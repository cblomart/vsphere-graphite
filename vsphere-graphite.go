package main

//go:generate git-version

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cblomart/vsphere-graphite/backend"
	"github.com/cblomart/vsphere-graphite/config"
	"github.com/cblomart/vsphere-graphite/vsphere"

	"github.com/takama/daemon"

	"code.cloudfoundry.org/bytefmt"
)

const (
	// name of the service
	name          = "vsphere-graphite"
	description   = "send vsphere stats to graphite"
	vcenterdefreg = "^VCENTER_.+=(?P<username>.+):(?P<password>.+)@(?P<hostname>.+)$"
)

var dependencies = []string{}

// Service has embedded daemon
type Service struct {
	daemon.Daemon
}

func queryVCenter(vcenter vsphere.VCenter, conf config.Configuration, channel *chan backend.Point, wg *sync.WaitGroup) {
	vcenter.Query(conf.Interval, conf.Domain, conf.ReplacePoint, conf.Properties, conf.VCenterResultLimit, conf.VCenterInstanceRatio, channel, wg)
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

	log.Println("Starting daemon:", path.Base(os.Args[0]))

	// find file location
	basename := path.Base(os.Args[0])
	configname := strings.TrimSuffix(basename, filepath.Ext(basename))
	location := "/etc/" + configname + ".json"
	if _, err := os.Stat(location); err != nil {
		location = configname + ".json"
		if _, err := os.Stat(location); err != nil {
			return "Could not find config location in '.' or '/etc'", err
		}
	}

	// read the configuration
	file, err := os.Open(location) // #nosec
	if err != nil {
		return "Could not open configuration file", err
	}
	jsondec := json.NewDecoder(file)
	conf := config.Configuration{}
	err = jsondec.Decode(&conf)
	if err != nil {
		return "Could not decode configuration file", err
	}

	// replace all by all properties
	all := false
	if conf.Properties == nil {
		all = true
	} else {
		for _, property := range conf.Properties {
			if strings.ToLower(property) == "all" {
				all = true
				break
			}
		}
	}
	if all {
		// Reset properties
		conf.Properties = []string{}
		// Fill it with all properties keys
		for propkey := range vsphere.Properties {
			conf.Properties = append(conf.Properties, propkey)
		}
	}
	log.Printf("main: requested properties %s", strings.Join(conf.Properties, ", "))

	// default flush size 1000
	if conf.FlushSize == 0 {
		conf.FlushSize = 1000
	}

	// default backend prefix to "vsphere"
	if len(conf.Backend.Prefix) == 0 {
		conf.Backend.Prefix = "vsphere"
	}

	// default resultlimit
	if conf.VCenterResultLimit == 0 {
		conf.VCenterResultLimit = 500000
	}

	// default result ratio
	if conf.VCenterInstanceRatio == 0 {
		conf.VCenterInstanceRatio = 3.0
	}

	if conf.CPUProfiling {
		f, err := os.OpenFile("/tmp/vsphere-graphite-cpu.pb.gz", os.O_RDWR|os.O_CREATE, 0600) // nolint: vetshadow
		if err != nil {
			log.Fatal("could not create CPU profile: ", err)
		}
		log.Println("Will write cpu profiling to: ", f.Name())
		if err := pprof.StartCPUProfile(f); err != nil {
			log.Fatal("could not start CPU profile: ", err)
		}
		defer pprof.StopCPUProfile()
	}

	//force backend values to environment variables if present
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
					log.Printf("setting config value %s from env. '%s'", s.Type().Field(i).Name, envval)
					f.SetString(envval)
				case "int":
					val, err := strconv.ParseInt(envval, 10, 64) // nolint: vetshadow
					if err == nil {
						log.Printf("setting config value %s from env. %d", s.Type().Field(i).Name, val)
						f.SetInt(val)
					}
				}
			}
		}
	}

	//force vcenter values to environment variables if present
	envvcenters := []*vsphere.VCenter{}
	validvcenter := regexp.MustCompile(vcenterdefreg)
	for _, e := range os.Environ() {
		// check if a vcenter definition
		if strings.HasPrefix(e, "VCENTER_") {
			if !validvcenter.MatchString(e) {
				log.Printf("cannot parse vcenter: '%s'\n", e)
				continue
			}
			matches := validvcenter.FindStringSubmatch(e)
			names := validvcenter.SubexpNames()
			username := ""
			password := ""
			hostname := ""
			for i, match := range matches {
				if i == 0 {
					continue
				}
				switch names[i] {
				case "username":
					username = match
				case "password":
					password = match
				case "hostname":
					hostname = match
				}
			}
			if len(username) == 0 {
				log.Printf("cannot find username in vcenter: '%s'", e)
				continue
			}
			if len(password) == 0 {
				log.Printf("cannot find password in vcenter: '%s'", e)
				continue
			}
			if len(hostname) == 0 {
				log.Printf("cannot find hostname in vcenter: '%s'", e)
				continue
			}
			vcenter := vsphere.VCenter{
				Username: username,
				Password: password,
				Hostname: hostname,
			}
			log.Printf("adding vcenter from env: %s", vcenter.ToString())
			envvcenters = append(envvcenters, &vcenter)
		}
	}
	if len(envvcenters) > 0 {
		conf.VCenters = envvcenters
		log.Println("config vcenter have been replaced by those in env")
	}

	for _, vcenter := range conf.VCenters {
		vcenter.Init(conf.Metrics)
	}

	queries, err := conf.Backend.Init()
	if err != nil {
		return "Could not initialize backend", err
	}
	defer conf.Backend.Disconnect()

	//check properties in function of backend support of metadata
	if !conf.Backend.HasMetadata() {
		properties := []string{}
		for _, confproperty := range conf.Properties {
			found := false
			for _, metricproperty := range vsphere.MetricProperties {
				if strings.EqualFold(confproperty, metricproperty) {
					found = true
					break
				}
			}
			if found {
				properties = append(properties, confproperty)
			}
		}
		conf.Properties = properties
		log.Printf("main: properties filtered to '%s' (no metadata in backend)", strings.Join(conf.Properties, ", "))
	}

	// Set up channel on which to send signal notifications.
	// We must use a buffered channel or risk missing the signal
	// if we're not ready to receive when the signal is sent.
	interrupt := make(chan os.Signal, 1)
	//lint:ignore SA1016 in this case we want to quit
	signal.Notify(interrupt, os.Interrupt, os.Kill, syscall.SIGTERM) // nolint: megacheck

	// Set up a channel to receive the metrics
	metrics := make(chan backend.Point, conf.FlushSize)

	ticker := time.NewTicker(time.Second * time.Duration(conf.Interval))
	defer ticker.Stop()

	// Set up a ticker to collect metrics at given interval (except for non scheduled backend)
	if !conf.Backend.Scheduled() {
		ticker.Stop()
	} else {
		// Start retriveing and sending metrics
		log.Println("Retrieving metrics")
		for _, vcenter := range conf.VCenters {
			go queryVCenter(*vcenter, conf, &metrics, nil)
		}
	}

	// Memory statistics
	var memstats runtime.MemStats
	// timer to execute memory collection
	memtimer := time.NewTimer(time.Second * time.Duration(10))
	// channel to cleanup
	cleanup := make(chan bool, 1)

	// buffer for points to send
	pointbuffer := make([]*backend.Point, conf.FlushSize)
	bufferindex := 0

	for {
		select {
		case value := <-metrics:
			// reset timer as a point has been received.
			// do that in the main thread to avoid collisions
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
				t := make([]*backend.Point, bufferindex)
				copy(t, pointbuffer)
				ClearBuffer(pointbuffer)
				bufferindex = 0
				go conf.Backend.SendMetrics(t, false)
				log.Printf("sent %d logs to backend\n", len(t))
			}
		case request := <-*queries:
			go func() {
				// wait group for non scheduled metric retrival
				var wg sync.WaitGroup

				log.Println("adhoc metric retrieval")
				wg.Add(len(conf.VCenters))
				for _, vcenter := range conf.VCenters {
					go queryVCenter(*vcenter, conf, request.Request, &wg)
				}
				wg.Wait()
				close(*request.Request)
				cleanup <- true
			}()
		case <-ticker.C:
			// not doing go func as it will create threads itself
			log.Println("scheduled metric retrieval")
			for _, vcenter := range conf.VCenters {
				go queryVCenter(*vcenter, conf, &metrics, nil)
			}
		case <-memtimer.C:
			if !conf.Backend.Scheduled() {
				continue
			}
			// sent remaining values
			// copy to send point to appart buffer
			t := make([]*backend.Point, bufferindex)
			copy(t, pointbuffer)
			// clear main buffer
			ClearBuffer(pointbuffer)
			bufferindex = 0
			// send sent buffer
			go conf.Backend.SendMetrics(t, true)
			log.Printf("sent last %d logs to backend\n", len(t))
			// empty point buffer
			cleanup <- true
		case <-cleanup:
			go func() {
				runtime.GC()
				debug.FreeOSMemory()
				runtime.ReadMemStats(&memstats)
				log.Printf("memory usage: sys=%s alloc=%s\n", bytefmt.ByteSize(memstats.Sys), bytefmt.ByteSize(memstats.Alloc))
				log.Printf("go routines: %d", runtime.NumGoroutine())
				if conf.MEMProfiling {
					f, err := os.OpenFile("/tmp/vsphere-graphite-mem.pb.gz", os.O_RDWR|os.O_CREATE, 0600) // nolint.vetshaddow
					if err != nil {
						log.Fatal("could not create Mem profile: ", err)
					}
					defer f.Close()
					log.Println("Will write mem profiling to: ", f.Name())
					if err := pprof.WriteHeapProfile(f); err != nil {
						log.Fatal("could not write Mem profile: ", err)
					}
					if err := f.Close(); err != nil {
						log.Fatal("could close Mem profile: ", err)
					}
				}
			}()
		case killSignal := <-interrupt:
			log.Println("Got signal:", killSignal)
			if bufferindex > 0 {
				conf.Backend.SendMetrics(pointbuffer[:bufferindex], true)
				log.Printf("Sent %d logs to backend", bufferindex)
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

func main() {
	log.Printf("Version information: %s - %s@%s (%s)", gitTag, gitShortCommit, gitBranch, gitStatus)
	srv, err := daemon.New(name, description, daemon.SystemDaemon, dependencies...)
	if err != nil {
		log.Println("Error: ", err)
		os.Exit(1)
	}
	service := &Service{srv}
	status, err := service.Manage()
	if err != nil {
		log.Println(status, "Error: ", err)
		os.Exit(1)
	}
	fmt.Println(status)
}
