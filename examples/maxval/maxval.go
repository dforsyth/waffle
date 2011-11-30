package main

import (
	"bufio"
	"encoding/gob"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
	"waffle"
)

type MaxValueVertex struct {
	waffle.VertexBase
	Value int
	Max   int
}

type MaxValueMsg struct {
	waffle.MsgBase
	Value int
}

// Load vertices from generated data file
type MaxValueLoader struct {
	basePath string
}

func (l *MaxValueLoader) AssignLoad(workers []string, files []string) map[string][]string {
	assign := make(map[string][]string)

	// XXX ghetto for testing
	for _, hostPort := range workers {
		assign[hostPort] = files
		for _, path := range assign[hostPort] {
			log.Printf("assigned loading: %s -> %s", path, hostPort)
		}
		break
	}

	return assign
}

func vertexBuilder(id, val string) *MaxValueVertex {
	v := &MaxValueVertex{}
	v.SetVertexId(strings.TrimSpace(id))
	if val, err := strconv.Atoi(strings.TrimSpace(val)); err != nil {
		return nil
	} else {
		v.Value = val
	}
	return v
}

func (l *MaxValueLoader) Load(w *waffle.Worker, filePath string) (loaded uint64, err error) {
	var file *os.File
	if file, err = os.Open(path.Join(l.basePath, filePath)); err != nil {
		return 0, err
	}
	reader := bufio.NewReader(file)

	var line string
	for {
		if line, err = reader.ReadString('\n'); err != nil {
			break
		}
		if line[0] == '#' {
			// comment line
			continue
		}
		split := strings.Split(line, "\t")
		v := vertexBuilder(split[0], split[1])
		if v == nil {
			return loaded, errors.New("bad vertex load")
		}
		for _, val := range split[2:] {
			e := &waffle.EdgeBase{}
			e.SetTarget(strings.TrimSpace(val))
			v.AddOutEdge(e)
		}
		v.SetActive(true)
		w.AddVertex(v)
		loaded++
	}
	if err == io.EOF {
		err = nil
	}
	return
}

// Writes max value to stdout
type MaxValueResultWriter struct {

}

func (rw *MaxValueResultWriter) WriteResults(w *waffle.Worker) error {
	max := 0
	for _, p := range w.Partitions() {
		for _, v := range p.Vertices() {
			mvv := v.(*MaxValueVertex)
			if mvv.Max > max {
				max = mvv.Max
			}
		}
	}
	fmt.Printf("Max value: %d\n", max)
	return nil
}

// Combine to a single max value message
type MaxValueCombiner struct {

}

func (c *MaxValueCombiner) Combine(msgs []waffle.Msg) []waffle.Msg {
	if len(msgs) == 0 {
		return msgs
	}
	maxMsg := msgs[0].(*MaxValueMsg)
	for _, msg := range msgs {
		if msg.(*MaxValueMsg).Value > maxMsg.Value {
			maxMsg = msg.(*MaxValueMsg)
		}
	}
	return []waffle.Msg{maxMsg}
}

// Do work
func (v *MaxValueVertex) Compute(msgs []waffle.Msg) {
	max := 0
	for _, msg := range msgs {
		val := msg.(*MaxValueMsg).Value
		if val > max {
			max = val
		}
	}
	if v.Value > max {
		max = v.Value
	}
	if max > v.Max {
		v.Max = max
		for _, e := range v.OutEdges() {
			v.SendMessageTo(e.Target(), &MaxValueMsg{Value: v.Max})
		}
	}
	v.VoteToHalt()
}

var master bool
var host, port, maddr, loadDir, persistDir string
var minWorkers uint64

func filesToLoad(dir string) ([]string, error) {
	files, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0)
	for _, file := range files {
		if !strings.HasSuffix(file.Name, ".data") {
			continue
		}
		paths = append(paths, file.Name)
	}
	log.Println("files to load:")
	for _, path := range paths {
		log.Printf("->%s", path)
	}
	return paths, nil
}

func main() {

	flag.BoolVar(&master, "master", false, "node is master")
	flag.StringVar(&maddr, "maddr", "127.0.0.1:50000", "master address")
	flag.StringVar(&port, "port", "50000", "node port")
	flag.StringVar(&host, "host", "127.0.0.1", "node address")
	flag.Uint64Var(&minWorkers, "minWorkers", 2, "min workers")
	flag.StringVar(&loadDir, "loadDir", "testdata", "data load path")
	flag.StringVar(&persistDir, "persistDir", "persist", "data persist path")

	flag.Parse()

	gob.Register(&MaxValueVertex{})
	gob.Register(&MaxValueMsg{})

	persister := waffle.NewGobPersister(persistDir)
	loader := &MaxValueLoader{basePath: loadDir}

	if master {
		m := waffle.NewMaster(host, port)

		m.Config.JobId = "maxval-" + time.UTC().String()
		m.Config.MinWorkers = minWorkers
		m.Config.HeartbeatInterval = 10 * 1e9

		m.SetRpcClient(waffle.NewGobMasterRPCClient())
		m.SetRpcServer(waffle.NewGobMasterRPCServer())
		m.SetPersister(persister)
		m.SetLoader(loader)
		m.SetCheckpointFn(func(checkpoint uint64) bool {
			return false
		})
		if paths, err := filesToLoad(loadDir); err != nil {
			panic(err)
		} else {
			m.SetLoadFiles(paths)
		}
		m.Run()
	} else {
		w := waffle.NewWorker(host, port)
		w.Config.MessageThreshold = 1000
		w.Config.VertexThreshold = 1000

		w.Config.MasterHost, w.Config.MasterPort, _ = net.SplitHostPort(maddr)

		w.SetRpcClient(waffle.NewGobWorkerRPCClient())
		w.SetRpcServer(waffle.NewGobWorkerRPCServer())
		w.SetLoader(loader)
		w.SetPersister(persister)
		w.SetResultWriter(&MaxValueResultWriter{})
		w.AddCombiner(&MaxValueCombiner{})
		w.Run()
	}
}
