package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path"
	"reflect"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cblomart/vsphere-graphite/backend"
	"github.com/cblomart/vsphere-graphite/config"
	"github.com/cblomart/vsphere-graphite/utils"
	"github.com/cblomart/vsphere-graphite/vsphere"

	"github.com/takama/daemon"

	"code.cloudfoundry.org/bytefmt"
)

const (
	// name of the service
	name        = "vsphere-graphite"
	description = "send vsphere stats to graphite"
)

var dependencies = []string{}

var stdlog, errlog *log.Logger

var commit, tag string

// Service has embedded daemon
type Service struct {
	daemon.Daemon
}

func queryVCenter(vcenter vsphere.VCenter, conf config.Configuration, channel *chan backend.Point, done *chan bool) {
	vcenter.Query(conf.Interval, conf.Domain, conf.Properties, channel, done)
}

// Manage by daemon commands or run the daemon
func (service *Service) Manage() (string, error) {

	usage := "Usage: vsphere-graphite install | remove | start | stop | status"

	// if received any kind of command, do it
	if len(os.Args) > 1 {
		command := os.Args[1]
		text := usage
		var err error
		switch command {
		case "install":
			text, err = service.Install()
		case "remove":
			text, err = service.Remove()
		case "start":
			text, err = service.Start()
		case "stop":
			text, err = service.Stop()
		case "status":
			text, err = service.Status()
		}
		return text, err
	}

	stdlog.Println("Starting daemon:", path.Base(os.Args[0]))

	// read the configuration
	file, err := os.Open("/etc/" + path.Base(os.Args[0]) + ".json")
	if err != nil {
		return "Could not open configuration file", err
	}
	jsondec := json.NewDecoder(file)
	conf := config.Configuration{}
	err = jsondec.Decode(&conf)
	if err != nil {
		return "Could not decode configuration file", err
	}

	// defaults to all properties
	if conf.Properties == nil {
		conf.Properties = []string{"all"}
	}

	if conf.FlushSize == 0 {
		conf.FlushSize = 1000
	}

	if conf.CPUProfiling {
		f, err := os.OpenFile("/tmp/vsphere-graphite-cpu.pb.gz", os.O_RDWR|os.O_CREATE, 0600) // nolint: vetshadow
		if err != nil {
			log.Fatal("could not create CPU profile: ", err)
		}
		stdlog.Println("Will write cpu profiling to: ", f.Name())
		if err := pprof.StartCPUProfile(f); err != nil {
			log.Fatal("could not start CPU profile: ", err)
		}
		defer pprof.StopCPUProfile()
	}

	//force backend values to environement varialbles if present
	s := reflect.ValueOf(conf.Backend).Elem()
	numfields := s.NumField()
	for i := 0; i < numfields; i++ {
		f := s.Field(i)
		if f.CanSet() {
			//exported field
			envname := strings.ToUpper(s.Type().Name() + "_" + s.Type().Field(i).Name)
			envval := os.Getenv(envname)
			if len(envval) > 0 {
				//environment variable set with name
				switch ftype := f.Type().Name(); ftype {
				case "string":
					f.SetString(envval)
				case "int":
					val, err := strconv.ParseInt(envval, 10, 64) // nolint: vetshadow
					if err == nil {
						f.SetInt(val)
					}
				}
			}
		}
	}

	for _, vcenter := range conf.VCenters {
		vcenter.Init(conf.Metrics, stdlog, errlog)
	}

	err = conf.Backend.Init(stdlog, errlog)
	if err != nil {
		return "Could not initialize backend", err
	}
	defer conf.Backend.Disconnect()

	// Set up channel on which to send signal notifications.
	// We must use a buffered channel or risk missing the signal
	// if we're not ready to receive when the signal is sent.
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, os.Kill, syscall.SIGTERM) // nolint: megacheck

	// Set up a channel to receive the metrics
	metrics := make(chan backend.Point, conf.FlushSize)
	runQuery := make(chan bool, 1)
	doneQuery := make(chan bool, 1)
	metricsProm := make(chan backend.Point)

	ticker := time.NewTicker(time.Second * time.Duration(conf.Interval))
	defer ticker.Stop()

	// Set up a ticker to collect metrics at givent interval (except for Prometheus which is Pull)
	if conf.Backend.Type == "prometheus" {
		stdlog.Println("Init Prometheus")
		err = conf.Backend.InitPrometheus(&runQuery, &doneQuery, &metricsProm)
		if err != nil {
			return "Init Prometheus failed", err
		}
		ticker.Stop()
	} else {
		// Start retriveing and sending metrics
		stdlog.Println("Retrieving metrics")
		for _, vcenter := range conf.VCenters {
			go queryVCenter(*vcenter, conf, &metrics, &doneQuery)
		}
	}

	// Memory statisctics
	var memstats runtime.MemStats
	// timer to execute memory collection
	memtimer := time.NewTimer(time.Second * time.Duration(10))

	// buffer for points to send
	pointbuffer := make([]*backend.Point, conf.FlushSize)
	bufferindex := 0

	for {
		select {
		case value := <-metrics:
			// reset timer as a point has been revieved
			if !memtimer.Stop() {
				select {
				case <-memtimer.C:
				default:
				}
			}
			memtimer.Reset(time.Second * time.Duration(5))
			pointbuffer[bufferindex] = &value
			bufferindex++
			if bufferindex == len(pointbuffer) {
				conf.Backend.SendMetrics(pointbuffer)
				stdlog.Printf("Sent %d logs to backend", bufferindex)
				ClearBuffer(pointbuffer)
				bufferindex = 0
			}
		case <-runQuery:
			stdlog.Println("Retrieving metrics")
			for _, vcenter := range conf.VCenters {
				go queryVCenter(*vcenter, conf, &metricsProm, &doneQuery)
			}
		case <-ticker.C:
			stdlog.Println("Retrieving metrics")
			for _, vcenter := range conf.VCenters {
				go queryVCenter(*vcenter, conf, &metrics, &doneQuery)
			}
		case <-memtimer.C:
			// sent remaining values
			conf.Backend.SendMetrics(pointbuffer)
			stdlog.Printf("Sent %d logs to backend", bufferindex)
			bufferindex = 0
			ClearBuffer(pointbuffer)
			runtime.GC()
			debug.FreeOSMemory()
			runtime.ReadMemStats(&memstats)
			stdlog.Printf("Memory usage : sys=%s alloc=%s\n", bytefmt.ByteSize(memstats.Sys), bytefmt.ByteSize(memstats.Alloc))
			if conf.MEMProfiling {
				f, err := os.OpenFile("/tmp/vsphere-graphite-mem.pb.gz", os.O_RDWR|os.O_CREATE, 0600) // nolin.vetshaddow
				defer f.Close()
				if err != nil {
					log.Fatal("could not create Mem profile: ", err)
				}
				stdlog.Println("Will write mem profiling to: ", f.Name())
				if err := pprof.WriteHeapProfile(f); err != nil {
					log.Fatal("could not write Mem profile: ", err)
				}
			}
		case killSignal := <-interrupt:
			stdlog.Println("Got signal:", killSignal)
			if bufferindex > 0 {
				conf.Backend.SendMetrics(pointbuffer[:bufferindex])
				stdlog.Printf("Sent %d logs to backend", bufferindex)
			}
			if killSignal == os.Interrupt {
				return "Daemon was interrupted by system signal", nil
			}
			return "Daemon was killed", nil
		}
	}
}

// ClearBuffer : set all values in pointer array to nil
func ClearBuffer(buffer []*backend.Point) {
	for i := 0; i < len(buffer); i++ {
		buffer[i] = nil
	}
}

func init() {
	stdlog = log.New(os.Stdout, "", log.Ldate|log.Ltime)
	errlog = log.New(os.Stderr, "", log.Ldate|log.Ltime)
	utils.Init(stdlog, errlog)
}

func main() {
	if len(commit) == 0 && len(tag) == 0 {
		stdlog.Println("No version information")
	} else {
		stdlog.Print("Version information")
		if len(commit) > 0 {
			stdlog.Print(" - Commit: ", commit)
		}
		if len(tag) > 0 {
			stdlog.Println(" - Version: ", tag)
		}
		stdlog.Print("\n")
	}
	srv, err := daemon.New(name, description, dependencies...)
	if err != nil {
		errlog.Println("Error: ", err)
		os.Exit(1)
	}
	service := &Service{srv}
	status, err := service.Manage()
	if err != nil {
		errlog.Println(status, "Error: ", err)
		os.Exit(1)
	}
	fmt.Println(status)
}
