package backend

import (
	"github.com/cblomart/vsphere-graphite/backend/thininfluxclient"
	"github.com/fluent/fluent-logger-golang/fluent"

	influxclient "github.com/influxdata/influxdb1-client/v2"
	graphite "github.com/marpaia/graphite-golang"
	kafka "github.com/segmentio/kafka-go"
	elastic "gopkg.in/olivere/elastic.v5"
)

// Config : storage backend
type Config struct {
	Hostname       string
	ValueField     string
	Database       string
	Username       string
	Password       string
	Type           string
	Prefix         string
	Port           int
	NoArray        bool
	Encrypted      bool
	carbon         *graphite.Graphite
	influx         *influxclient.Client
	thininfluxdb   *thininfluxclient.ThinInfluxClient
	elastic        *elastic.Client
	fluent         *fluent.Fluent
	promCollectors map[string]*PrometheusBackend
	kafka          *kafka.Writer
}

// PrometheusBackend : Extend prometheus.Collector
type PrometheusBackend struct {
	Config *Config
	Target string
}
