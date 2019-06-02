package backend

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/cblomart/vsphere-graphite/utils"

	"net/http"

	"github.com/cblomart/vsphere-graphite/backend/thininfluxclient"
	"github.com/fluent/fluent-logger-golang/fluent"

	influxclient "github.com/influxdata/influxdb1-client/v2"
	graphite "github.com/marpaia/graphite-golang"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	elastic "gopkg.in/olivere/elastic.v5"
)

// Channels are use for unscheduled backend
type Channels struct {
	Request *chan Point
	Done    *chan bool
}

// Backend Interface
type Backend interface {
	Init(config Config) error
	Disconnect()
	SendMetrics(metrics []*Point)
}

// MapStr represents a json node (string reference to an object)
type MapStr map[string]interface{}

const (
	// Graphite name of the graphite backend
	Graphite = "graphite"
	// InfluxDB name of the influx db backend
	InfluxDB = "influxdb"
	// ThinInfluxDB name of the thin influx db backend
	ThinInfluxDB = "thininfluxdb"
	// InfluxTag is the tag for influxdb
	InfluxTag = "influx"
	// Elastic name of the elastic backend
	Elastic = "elastic"
	// Prometheus name of the prometheus backend
	Prometheus = "prometheus"
	// Fluentd name of the fluentd backend
	Fluentd = "fluentd"
	// ThinPrometheus name of the thin prometheus backend
	ThinPrometheus = "thinprometheus"
)

var (
	queries *chan Channels
	prefix  string
)

// Init : initialize a backend
func (backend *Config) Init() (*chan Channels, error) {
	q := make(chan Channels)
	queries = &q
	prefix = backend.Prefix
	if len(backend.ValueField) == 0 {
		// for compatibility reason with previous version
		// can now be changed in the config file.
		// the default can later be changed to another value.
		// most probably "value" (lower case)
		backend.ValueField = "Value"
	}
	switch backendType := strings.ToLower(backend.Type); backendType {
	case Graphite:
		// Initialize Graphite
		log.Printf("backend %s: intializing\n", backendType)
		carbon, err := graphite.NewGraphite(backend.Hostname, backend.Port)
		if err != nil {
			log.Printf("backend %s: error connecting to graphite - %s\n", backendType, err)
			return queries, err
		}
		backend.carbon = carbon
		return queries, nil
	case InfluxDB:
		//Initialize Influx DB
		log.Printf("backend %s: intializing\n", backendType)
		protocol := "http"
		if backend.Encrypted {
			protocol = "https"
		}
		influxclt, err := influxclient.NewHTTPClient(influxclient.HTTPConfig{
			Addr:     fmt.Sprintf("%s://%s:%d", protocol, backend.Hostname, backend.Port),
			Username: backend.Username,
			Password: backend.Password,
		})
		if err != nil {
			log.Printf("backend %s: error connecting - %s\n", backendType, err)
			return queries, err
		}
		backend.influx = &influxclt
		return queries, nil
	case ThinInfluxDB:
		//Initialize thin Influx DB client
		log.Printf("backend %s: initializing\n", backendType)
		thininfluxclt, err := thininfluxclient.NewThinInlfuxClient(backend.Hostname, backend.Port, backend.Database, backend.Username, backend.Password, "s", backend.Encrypted)
		if err != nil {
			log.Printf("backend %s: error creating client - %s\n", backendType, err)
			return queries, err
		}
		backend.thininfluxdb = &thininfluxclt
		return queries, nil
	case Elastic:
		//Initialize Elastic client
		log.Printf("backend %s: initializing\n", backendType)
		elasticindex := backend.Database
		if len(elasticindex) > 0 {
			elasticindex = elasticindex + "-" + time.Now().Format("2006.01.02")
		} else {
			log.Printf("backend %s: Database not specified in vsphere-graphite.json (used as index)\n", backendType)
		}
		log.Printf("backend %s: %s:%d/%s\n", backendType, backend.Hostname, backend.Port, elasticindex)
		protocol := "http"
		if backend.Encrypted {
			protocol = "https"
		}
		elasticclt, err := elastic.NewClient(
			elastic.SetURL(protocol+"://"+backend.Hostname+":"+strconv.Itoa(backend.Port)),
			elastic.SetRetrier(elastic.NewBackoffRetrier(elastic.NewSimpleBackoff(100, 500, 2000, 5000, 10000))), // 5 retries with fixed delay of 100ms, 500ms, 2s, 5s, and 10s,
			elastic.SetScheme(protocol),
			elastic.SetBasicAuth(backend.Username, backend.Password),
			elastic.SetSniff(true))
		if err != nil {
			log.Printf("backend %s: error creating client - %s\n", backendType, err)
			return queries, err
		}
		backend.elastic = elasticclt
		return queries, CreateIndexIfNotExists(backend.elastic, elasticindex)
	case Prometheus:
		//Initialize Prometheus client
		log.Printf("backend %s: initializing\n", backendType)
		registry := prometheus.NewRegistry()
		err := registry.Register(backend)
		if err != nil {
			log.Printf("backend %s: error creating registry - %s\n", backendType, err)
			return queries, err
		}
		http.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{ErrorHandling: promhttp.ContinueOnError}))
		go func() error {
			address := ""
			if len(backend.Hostname) > 0 {
				address = backend.Hostname
			}
			if backend.Port > 0 {
				address += ":" + utils.ValToString(backend.Port, "", false)
			} else {
				address += ":9155"
			}
			log.Printf("backend %s: starting listener on %s\n", backendType, address)
			err := http.ListenAndServe(address, nil)
			// list and serve allways retruns non nil error
			log.Printf("backend %s: listener result - %s\n", backendType, err)
			return err
		}()
		return queries, nil
	case Fluentd:
		//Initialize Influx DB
		log.Printf("backend %s: initializing\n", backendType)
		fluentclt, err := fluent.New(fluent.Config{FluentPort: backend.Port, FluentHost: backend.Hostname, MarshalAsJSON: true})
		if err != nil {
			log.Printf("backend %s: error connecting - %s\n", backendType, err)
			return queries, err
		}
		backend.fluent = fluentclt
		return queries, nil
	case ThinPrometheus:
		//Initialize Thin Prometheus
		log.Printf("backend %s: initializing\n", backendType)
		client, err := NewThinPrometheusClient(backend.Hostname, backend.Port)
		if err != nil {
			log.Printf("backend %s: error connecting - %s\n", backendType, err)
			return nil, err
		}
		go func() {
			err := client.ListenAndServe()
			if err != nil {
				log.Printf("backend %s: error starting listener - %s", backendType, err)
			}
		}()
		return queries, nil
	default:
		log.Printf("backend %s: unknown backend\n", backendType)
		return queries, errors.New("backend " + backendType + " unknown.")
	}
}

// Clean : take actions on backend when cycle finished
func (backend *Config) Clean() {
	switch backendType := strings.ToLower(backend.Type); backendType {
	case Elastic:
		// flush index
		// get or create index
		elasticindex := backend.Database + "-" + time.Now().Format("2006.01.02")
		err := CreateIndexIfNotExists(backend.elastic, elasticindex)
		if err != nil {
			log.Printf("backend %s: could not create index - %s\n", backendType, err)
			return
		}
		// Succeeded actions
		log.Printf("backend %s: flushing index %s", backendType, elasticindex)
		_, err = backend.elastic.Flush().Index(elasticindex).Do(context.Background())
		if err != nil {
			log.Printf("backend %s: error flushing indices - %s\n", backendType, err)
		}
	default:
		return
	}
}

// Disconnect : disconnect from backend
func (backend *Config) Disconnect() {
	switch backendType := strings.ToLower(backend.Type); backendType {
	case Graphite:
		// Disconnect from graphite
		log.Println("Disconnecting from graphite")
		err := backend.carbon.Disconnect()
		if err != nil {
			log.Println("Error disconnecting from graphite: ", err)
		}
	case InfluxDB:
		// Disconnect from influxdb
		log.Println("Disconnecting from influxdb")
	case ThinInfluxDB:
		// Disconnect from thin influx db
		log.Println("Disconnecting from thininfluxdb")
	case Elastic:
		// Disconnect from Elastic
		log.Println("Disconnecting from elastic")
	case Prometheus:
		// Stop Exporting Prometheus Metrics
		log.Println("Stopping Prometheus exporter")
	case Fluentd:
		// Disconnect from Elastic
		log.Println("Disconnecting from fluent")
	case ThinPrometheus:
		// Stop exporting Prometheus metrics
		log.Println("Disconnect ThinPrometheus exporter")
	default:
		log.Println("Backend " + backendType + " unknown.")
	}
}

// SendMetrics : send metrics to backend
func (backend *Config) SendMetrics(metrics []*Point, cleanup bool) {
	switch backendType := strings.ToLower(backend.Type); backendType {
	case Graphite:
		var graphiteMetrics []graphite.Metric
		for _, point := range metrics {
			if point == nil {
				continue
			}
			//key := "vsphere." + vcName + "." + entityName + "." + name + "." + metricName
			key := fmt.Sprintf("%s.%s.%s.%s.%s.%s.%s", backend.Prefix, point.VCenter, point.ObjectType, point.ObjectName, point.Group, point.Counter, point.Rollup)
			if len(point.Instance) > 0 {
				key += "." + strings.ToLower(strings.Replace(point.Instance, ".", "_", -1))
			}
			graphiteMetrics = append(graphiteMetrics, graphite.Metric{Name: key, Value: strconv.FormatInt(point.Value, 10), Timestamp: point.Timestamp})
		}
		err := backend.carbon.SendMetrics(graphiteMetrics)
		if err != nil {
			/* remove connection retry */
			//log.Printf("backend %s : Error sending metrics (trying to reconnect) - %s\n", backendType, err)
			//err := backend.carbon.Connect()
			//if err != nil {
			//	log.Printf("backend %s : could not connect to graphite - %s\n", err)
			//}
			log.Printf("backend %s: could not send metrics - %s\n", backendType, err)
		}
	case InfluxDB:
		//Influx batch points
		bp, err := influxclient.NewBatchPoints(influxclient.BatchPointsConfig{
			Database:  backend.Database,
			Precision: "s",
		})
		if err != nil {
			log.Printf("backend %s: error creating influx batchpoint - %s\n", backendType, err)
			break
		}
		for _, point := range metrics {
			if point == nil {
				continue
			}
			key := fmt.Sprintf("%s_%s_%s", point.Group, point.Counter, point.Rollup)
			tags := point.GetTags(backend.NoArray, "\\,")
			fields := make(map[string]interface{})
			fields[backend.ValueField] = point.Value
			pt, err := influxclient.NewPoint(key, tags, fields, time.Unix(point.Timestamp, 0)) // nolint: vetshadow
			if err != nil {
				log.Printf("backend %s: could not create influxdb point - %s\n", backendType, err)
				continue
			}
			bp.AddPoint(pt)
		}
		err = (*backend.influx).Write(bp)
		if err != nil {
			log.Printf("backend %s: error sending metrics - %s\n", backendType, err)
		}
	case ThinInfluxDB:
		lines := []string{}
		for _, point := range metrics {
			if point == nil {
				continue
			}
			lines = append(lines, point.ToInflux(backend.NoArray, backend.ValueField))
		}
		/** simplify sent lines to thin influx
		count := 3
		for count > 0 {
			err := backend.thininfluxdb.Send(lines)
			if err != nil {
				log.Printf("backend %s: error sending metrics - %s\n", backendType, err)
				if err.Error() == "Server Busy: timeout" {
					log.Println("waiting .5 second to continue")
					time.Sleep(500 * time.Millisecond)
					count--
				} else {
					break
				}
			} else {
				break
			}
		}
		**/
		err := backend.thininfluxdb.Send(lines)
		if err != nil {
			log.Printf("backend %s: error sending metrics - %s\n", backendType, err)
		}
	case Elastic:
		elasticindex := backend.Database + "-" + time.Now().Format("2006.01.02")
		err := CreateIndexIfNotExists(backend.elastic, elasticindex)
		if err != nil {
			log.Printf("backend %s: could not create index - %s\n", backendType, err)
			break
		}
		bulkRequest := backend.elastic.Bulk()
		for _, point := range metrics {
			indexReq := elastic.NewBulkIndexRequest().Index(elasticindex).Type("doc").Doc(point).UseEasyJSON(true)
			bulkRequest = bulkRequest.Add(indexReq)
		}
		bulkResponse, err := bulkRequest.Do(context.Background())
		if err != nil {
			log.Printf("backend %s: bulk insert failed - %s\n", backendType, err)
			break
		}
		// Succeeded actions
		succeeded := bulkResponse.Succeeded()
		log.Printf("backend %s: %d logs successfully indexed\n", backendType, len(succeeded))
		/** only flush index at the end of cycle - see Clean function
		_, err = backend.elastic.Flush().Index(elasticindex).Do(context.Background())
		if err != nil {
			log.Printf("backend %s: errr flushing data - %s\n", backendType, err)
			return
		}
		log.Printf("backend %s: elastic indexing flushed", backendType)
		**/
	case Prometheus:
		// Prometheus doesn't need to send metrics
	case Fluentd:
		for _, point := range metrics {
			if point == nil {
				continue
			}
			err := backend.fluent.Post(backend.Prefix, *point)
			if err != nil {
				log.Printf("bakcend %s: failed to post point - %s\n", backendType, err)
			}
		}
	case ThinPrometheus:
		// Thin Prometheus doesn't need to send metrics
	default:
		log.Printf("backend %s: unknown", backendType)
	}
	if cleanup {
		backend.Clean()
	}
}

// Scheduled indicates that the metric collection needs to be scheduled for the backend
func (backend *Config) Scheduled() bool {
	switch backendType := strings.ToLower(backend.Type); backendType {
	case Prometheus:
		return false
	case ThinPrometheus:
		return false
	default:
		return true
	}
}

// HasMetadata indicates that the backend supports metadata
func (backend *Config) HasMetadata() bool {
	switch backendType := strings.ToLower(backend.Type); backendType {
	case Graphite:
		return false
	default:
		return true
	}
}
