package backend

// InitPrometheus : Set some channels to notify other theads when using Prometheus
import (
	"fmt"
	"log"

	"github.com/prometheus/client_golang/prometheus"
)

// Describe : Implementation of Prometheus Collector.Describe
func (backend *PrometheusBackend) Describe(ch chan<- *prometheus.Desc) {
	prometheus.NewGauge(prometheus.GaugeOpts{Name: "Dummy", Help: "Dummy"}).Describe(ch)
}

// Collect : Implementation of Prometheus Collector.Collect
func (backend *PrometheusBackend) Collect(ch chan<- prometheus.Metric) {

	log.Printf("prometheus: requesting metrics for %s\n", backend.Target)

	request := make(chan Point, 10000)
	channels := Channels{Request: &request, Target: backend.Target}

	select {
	case *queries <- channels:
		log.Println("prometheus: requested metrics")
	default:
		log.Println("prometheus: query buffer full. discarding request")
		return
	}

	// points received
	points := 0
	for point := range *channels.Request {
		// increase points
		points++
		// send point to prometheus
		backend.PrometheusSend(ch, point)
	}
	log.Printf("prometheus: sent %d points", points)
}

//PrometheusSend sends a point to prometheus
func (backend *PrometheusBackend) PrometheusSend(ch chan<- prometheus.Metric, point Point) {
	tags := point.GetTags(backend.Config.NoArray, ",")
	labelNames := make([]string, len(tags))
	labelValues := make([]string, len(tags))
	i := 0
	for key, value := range tags {
		labelNames[i] = key
		labelValues[i] = value
		i++
	}
	key := fmt.Sprintf("%s_%s_%s_%s", backend.Config.Prefix, point.Group, point.Counter, point.Rollup)
	desc := prometheus.NewDesc(key, "vSphere collected metric", labelNames, nil)
	metric, err := prometheus.NewConstMetric(desc, prometheus.GaugeValue, float64(point.Value), labelValues...)
	if err != nil {
		log.Println("Error creating prometheus metric")
	}
	ch <- metric
}
