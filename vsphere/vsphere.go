package vsphere

import (
	"fmt"
	"log"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vmware/govmomi/vim25/soap"

	"github.com/cblomart/vsphere-graphite/backend"
	"github.com/cblomart/vsphere-graphite/utils"

	"golang.org/x/net/context"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

var cache Cache

// VCenter description
type VCenter struct {
	Hostname     string
	Username     string
	Password     string
	MetricGroups []*MetricGroup
}

// ToString : represents the vcenter as a string
func (vcenter *VCenter) ToString() string {
	return fmt.Sprintf("%s:*@%s", vcenter.Username, vcenter.Hostname)
}

// AddMetric : add a metric definition to a metric group
func (vcenter *VCenter) AddMetric(metric MetricDef, mtype string) {
	// find the metric group for the type
	var metricGroup *MetricGroup
	for _, tmp := range vcenter.MetricGroups {
		if tmp.ObjectType == mtype {
			metricGroup = tmp
			break
		}
	}
	// create a new group if needed
	if metricGroup == nil {
		metricGroup = &MetricGroup{ObjectType: mtype}
		vcenter.MetricGroups = append(vcenter.MetricGroups, metricGroup)
	}
	// check if Metric already present
	for _, tmp := range metricGroup.Metrics {
		if tmp.Key == metric.Key {
			// metric already in metric group
			return
		}
	}
	metricGroup.Metrics = append(metricGroup.Metrics, &metric)
}

// Connect : Connect to vcenter
func (vcenter *VCenter) Connect() (*govmomi.Client, error) {

	// prepare vcName
	vcName := strings.Split(vcenter.Hostname, ".")[0]

	// Prepare vCenter Connections
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log.Printf("vcenter %s: connecting\n", vcName)
	username := url.QueryEscape(vcenter.Username)
	password := url.QueryEscape(vcenter.Password)
	u, err := url.Parse("https://" + username + ":" + password + "@" + vcenter.Hostname + "/sdk")
	if err != nil {
		log.Printf("vcenter %s: could not parse vcenter url - %s\n", vcName, err)
		return nil, err
	}
	client, err := govmomi.NewClient(ctx, u, true)
	if err != nil {
		log.Printf("vcenter %s: could not connect to vcenter - %s\n", vcName, err)
		return nil, err
	}
	return client, nil
}

// InitMetrics : maps metric keys to requested metrics
func InitMetrics(metrics []*Metric, perfmanager *mo.PerformanceManager) {
	// build a map of perf key per metric string id
	metricToPerf := make(map[string]int32)
	for _, perf := range perfmanager.PerfCounter {
		key := fmt.Sprintf("%s.%s.%s", strings.ToLower(perf.GroupInfo.GetElementDescription().Key), strings.ToLower(perf.NameInfo.GetElementDescription().Key), strings.ToLower(string(perf.RollupType)))
		metricToPerf[key] = perf.Key
	}
	// fill in the metric key
	for _, metric := range metrics {
		for _, metricdef := range metric.Definition {
			if key, ok := metricToPerf[strings.ToLower(metricdef.Metric)]; ok {
				metricdef.Key = key
			}
		}
	}
}

// Init : initialize vcenter
func (vcenter *VCenter) Init(metrics []*Metric) {
	vcName := strings.Split(vcenter.Hostname, ".")[0]

	log.Printf("vcenter %s: initializing\n", vcName)
	// connect to vcenter
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client, err := vcenter.Connect()
	if err != nil {
		log.Printf("vcenter %s: could not connect to vcenter - %s\n", vcName, err)
		return
	}

	defer func() {
		log.Printf("vcenter %s: disconnecting\n", vcName)
		err := client.Logout(ctx) // nolint: vetshadow
		if err != nil {
			log.Printf("vcenter %s: error logging out - %s\n", vcName, err)
		}
	}()

	// get the performance manager
	var perfmanager mo.PerformanceManager
	err = client.RetrieveOne(ctx, *client.ServiceContent.PerfManager, nil, &perfmanager)
	if err != nil {
		log.Printf("vcenter %s: could not get performance manager - %s\n", vcName, err)
		return
	}

	InitMetrics(metrics, &perfmanager)
	// add the metric to be collected in vcenter
	for _, metric := range metrics {
		for _, metricdef := range metric.Definition {
			if metricdef.Key == 0 {
				log.Printf("vcenter %s: metric key was not found %s", vcName, metricdef.Metric)
				continue
			}
			for _, mtype := range metric.ObjectType {
				vcenter.AddMetric(*metricdef, mtype)
			}
		}
	}
	// initialize cache
	if cache == nil {
		cache = make(Cache)
	}
}

// Query : Query a vcenter
func (vcenter *VCenter) Query(interval int, domain string, replacepoint bool, properties []string, resultLimit int, instanceRatio float64, channel *chan backend.Point, wg *sync.WaitGroup) {
	defer func() {
		if wg != nil {
			wg.Done()
		}
	}()

	// prepare vcname
	vcName := strings.Replace(vcenter.Hostname, domain, "", -1)

	log.Printf("vcenter %s: setting up query inventory", vcName)

	// Create the context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Get the client
	client, err := vcenter.Connect()
	if err != nil {
		log.Printf("vcenter %s: could not connect to vcenter - %s\n", vcName, err)
		return
	}

	// wait to be properly connected to defer logout
	defer func() {
		log.Printf("vcenter %s: disconnecting\n", vcName)
		err := client.Logout(ctx) // nolint: vetshadow
		if err != nil {
			log.Printf("vcenter %s: error logging out - %s\n", vcName, err)
		}
	}()

	// Create the view manager
	var viewManager mo.ViewManager
	err = client.RetrieveOne(ctx, *client.ServiceContent.ViewManager, nil, &viewManager)
	if err != nil {
		log.Printf("vcenter %s: could not get view manager - %s\n", vcName, err)
		return
	}

	// Find all Datacenters accessed by the user
	datacenters := []types.ManagedObjectReference{}
	finder := find.NewFinder(client.Client, true)
	dcs, err := finder.DatacenterList(ctx, "*")
	if err != nil {
		log.Printf("vcenter %s: could not find a datacenter - %s\n", vcName, err)
		return
	}
	for _, child := range dcs {
		datacenters = append(datacenters, child.Reference())
	}

	// Get the object types from properties
	objectTypes := []string{}
	for _, property := range properties {
		if propval, ok := Properties[property]; ok {
			for objkey := range propval {
				objectTypes = append(objectTypes, objkey)
			}
		}
	}
	// Get interesting object types from specified queries
	// Complete object of interest with required metrics
	for _, group := range vcenter.MetricGroups {
		found := false
		for _, tmp := range objectTypes {
			if group.ObjectType == tmp {
				found = true
				break
			}
		}
		if !found {
			objectTypes = append(objectTypes, group.ObjectType)
		}
	}

	// Loop trough datacenters and create the intersting object reference list
	mors := []types.ManagedObjectReference{}
	for _, datacenter := range datacenters {
		// Create the CreateContentView request
		req := types.CreateContainerView{This: viewManager.Reference(), Container: datacenter, Type: objectTypes, Recursive: true}
		res, err := methods.CreateContainerView(ctx, client.RoundTripper, &req) // nolint: vetshadow
		if err != nil {
			log.Printf("vcenter %s: could not create container view - %s\n", vcName, err)
			continue
		}
		// Retrieve the created ContentView
		var containerView mo.ContainerView
		err = client.RetrieveOne(ctx, res.Returnval, nil, &containerView)
		if err != nil {
			log.Printf("vcenter %s: could not get container - %s\n", vcName, err)
			continue
		}
		// Add found object to object list
		mors = append(mors, containerView.View...)
	}

	if len(mors) == 0 {
		log.Printf("vcenter %s: no object of intereset in types %s\n", vcName, strings.Join(objectTypes, ", "))
		return
	}

	//object for property collection
	var objectSet []types.ObjectSpec
	for _, mor := range mors {
		objectSet = append(objectSet, types.ObjectSpec{Obj: mor, Skip: types.NewBool(false)})
	}

	//properties specifications
	reqProps := make(map[string][]string)
	//first fill in name for each object type and add connectionstate and powerstate
	for _, objType := range objectTypes {
		reqProps[objType] = []string{"name"}
		if objType == "VirtualMachine" || objType == "HostSystem" {
			reqProps[objType] = append(reqProps[objType], "runtime.connectionState")
			reqProps[objType] = append(reqProps[objType], "runtime.powerState")
		}
	}
	//complete with required properties
	for _, property := range properties {
		if typeProperties, ok := Properties[property]; ok {
			for objType, typeProperties := range typeProperties {
				for _, typeProperty := range typeProperties {
					found := false
					for _, prop := range reqProps[objType] {
						if prop == typeProperty {
							found = true
							break
						}
					}
					if !found {
						reqProperties := append(reqProps[objType], typeProperty)
						reqProps[objType] = reqProperties
					}
				}
			}
		}
	}
	propSet := []types.PropertySpec{}
	for objType, props := range reqProps {
		propSet = append(propSet, types.PropertySpec{Type: objType, PathSet: props})
	}

	//retrieve properties
	propreq := types.RetrieveProperties{SpecSet: []types.PropertyFilterSpec{{ObjectSet: objectSet, PropSet: propSet}}}
	propres, err := client.PropertyCollector().RetrieveProperties(ctx, propreq)
	if err != nil {
		log.Printf("vcenter %s: could not retrieve object names - %s\n", vcName, err)
		return
	}

	// fill in the sections from queried data
	refs := make([]string, len(propres.Returnval))
	for _, objectContent := range propres.Returnval {
		refs = append(refs, objectContent.Obj.Value)
		for _, Property := range objectContent.PropSet {
			if section, ok := PropertiesSections[Property.Name]; ok {
				cache.Add(vcName, section, objectContent.Obj.Value, Property.Val)
			} else {
				log.Printf("vcenter %s: unhandled property '%s' for %s whose type is '%T'\n", vcName, Property.Name, objectContent.Obj.Value, Property.Val)
			}
		}
	}

	// cleanup buffer
	cache.CleanAll(vcName, refs)

	// create a map to resolve metric names
	metricKeys := []string{}
	for _, metricgroup := range vcenter.MetricGroups {
		for _, metricdef := range metricgroup.Metrics {
			i := utils.ValToString(metricdef.Key, "", true)
			cache.Add(vcName, "metrics", i, metricdef.Metric)
			metricKeys = append(metricKeys, i)
		}
	}
	// empty metric to names
	cache.Clean(vcName, "metrics", metricKeys)

	//create a map to resolve vm to their ressourcepool
	poolvms := []string{}
	for mor, vmmors := range *cache.LookupMorefs(vcName, "vms") {
		// only parse resource pools
		if strings.HasPrefix(mor, "rp-") {
			// find the full path of the resource pool
			poolmor := mor
			pools := []string{}
			for {
				poolname := cache.GetString(vcName, "names", poolmor)
				if len(*poolname) == 0 {
					// could not find name
					log.Printf("vcenter %s: could not find name for resourcepool %s\n", vcName, poolmor)
					break
				}
				if *poolname == "Resources" {
					// ignore root resourcepool
					break
				}
				// add the name to the path
				pools = append(pools, *poolname)
				newmor := cache.GetString(vcName, "parents", poolmor)
				if len(*newmor) == 0 {
					// no parent pool found
					log.Printf("vcenter %s: could not find parent for resourcepool %s\n", vcName, *poolname)
					break
				}
				poolmor = *newmor
				if !strings.HasPrefix(poolmor, "rp-") {
					break
				}
			}
			utils.Reverse(pools)
			poolpath := strings.Join(pools, "/")
			for _, vmmor := range *vmmors {
				if vmmor.Type == "VirtualMachine" {
					poolvms = append(poolvms, vmmor.Value)
					// Check if value already correct
					curpath := cache.GetString(vcName, "poolpaths", vmmor.Value)
					if curpath != nil {
						if poolpath == *curpath {
							continue
						}
					}
					// assign it
					cache.Add(vcName, "poolpaths", vmmor.Value, poolpath)
				}
			}
		}
	}
	// cleanup poolpaths
	cache.Clean(vcName, "poolpaths", poolvms)

	// create a map to resolve datastore ids to their names
	datastoreids := []string{}
	for mor, dsurl := range *cache.LookupString(vcName, "urls") {
		// find the datastore id
		regex := regexp.MustCompile("/([A-Fa-f0-9-]+)/$")
		matches := regex.FindAllString(*dsurl, 1)
		datastoreid := ""
		if len(matches) == 1 {
			datastoreid = strings.Trim(matches[0], "/")
		}
		// if an id is found, search for the name
		name := cache.GetString(vcName, "names", mor)
		if name != nil {
			// add the value to the index of ids
			datastoreids = append(datastoreids, datastoreid)
			// add the value to the cache
			cache.Add(vcName, "datastoreids", datastoreid, *name)
		}
	}
	cache.Clean(vcName, "datastoreids", datastoreids)

	cache.Purge(vcName, "folders")

	// Create Queries from interesting objects and requested metrics
	queries := []types.PerfQuerySpec{}

	// Common parameters
	intervalID := int32(20)
	endTime := time.Now().Add(time.Duration(-1) * time.Second)
	startTime := endTime.Add(time.Duration(-interval-1) * time.Second)

	// Parse objects
	skipped := 0
	totalvms := 0
	totalhosts := 0
	for _, mor := range mors {
		// check connection state
		connectionState := cache.GetConnectionState(vcName, "connections", mor.Value)
		if connectionState != nil {
			if *connectionState != "connected" {
				skipped++
				continue
			}
		}
		// check power state
		powerState := cache.GetPowerState(vcName, "powers", mor.Value)
		if powerState != nil {
			if *powerState != "poweredOn" {
				skipped++
				continue
			}
		}
		switch mor.Type {
		case "HostSystem":
			totalhosts++
		case "VirtualMachine":
			totalvms++
		}
		metricIds := []types.PerfMetricId{}
		for _, metricgroup := range vcenter.MetricGroups {
			if metricgroup.ObjectType == mor.Type {
				for _, metricdef := range metricgroup.Metrics {
					metricIds = append(metricIds, types.PerfMetricId{CounterId: metricdef.Key, Instance: metricdef.Instances})
				}
			}
		}
		if len(metricIds) > 0 {
			queries = append(queries, types.PerfQuerySpec{Entity: mor, StartTime: &startTime, EndTime: &endTime, MetricId: metricIds, IntervalId: intervalID})
		}
	}

	// log skipped objects
	log.Printf("vcenter %s: skipped %d objects because they are either not connected or not powered on", vcName, skipped)
	// log total object referenced
	log.Printf("vcenter %s: %d objects (%d vm and %d hosts)", vcName, totalhosts+totalvms, totalvms, totalhosts)
	// Check that there is something to query
	querycount := len(queries)
	if querycount == 0 {
		log.Printf("vcenter %s: no queries created!", vcName)
		return
	}
	metriccount := 0
	for _, query := range queries {
		metriccount = metriccount + len(query.MetricId)
	}

	expCounters := math.Ceil(float64(metriccount) * instanceRatio)
	log.Printf("vcenter %s: queries generated", vcName)
	log.Printf("vcenter %s: %d queries\n", vcName, querycount)
	log.Printf("vcenter %s: %d total metricIds\n", vcName, metriccount)
	log.Printf("vcenter %s: %g total counter (accounting for %g instances ratio)\n", vcName, expCounters, instanceRatio)

	// separate in batches of queries if to avoid 500000 returned perf limit
	batches := math.Ceil(expCounters / float64(resultLimit))
	batchqueries := make([]*types.QueryPerf, int(batches))
	querieslen := len(queries)
	batchsize := int(math.Ceil(float64(querieslen) / batches))
	batchnum := 0
	for i := 0; i < querieslen; i += batchsize {
		end := i + batchsize
		if end > querieslen {
			end = querieslen
		}
		batchqueries[batchnum] = &types.QueryPerf{This: *client.ServiceContent.PerfManager, QuerySpec: queries[i:end]}
		log.Printf("vcenter %s: created batch %d from queries %d - %d", vcName, batchnum, i+1, end)
		batchnum++
	}
	log.Printf("vcenter %s: %d threads generated to execute queries", vcName, len(batchqueries))
	for i, query := range batchqueries {
		log.Printf("vcenter %s: thread %d requests %d metrics", vcName, i+1, len(query.QuerySpec))
	}

	// make each queries in separate functions
	// use a wait group to avoid exiting if all threads are not finished

	// create the wait group and declare the amount of threads
	var querieswaitgroup sync.WaitGroup
	querieswaitgroup.Add(len(batchqueries))

	// execute the threads
	for i, query := range batchqueries {
		go ExecuteQueries(ctx, i+1, client.RoundTripper, &cache, query, endTime.Unix(), replacepoint, domain, vcName, channel, &querieswaitgroup)
	}

	//wait fot the waitgroup
	querieswaitgroup.Wait()

}

// ExecuteQueries : Query a vcenter for performances
func ExecuteQueries(ctx context.Context, id int, r soap.RoundTripper, cache *Cache, queryperf *types.QueryPerf, timeStamp int64, replacepoint bool, domain string, vcName string, channel *chan backend.Point, wg *sync.WaitGroup) {

	// Starting information
	requestedcount := len(queryperf.QuerySpec)
	log.Printf("vcenter %s thread %d: requesting %d metrics\n", vcName, id, requestedcount)

	// tell the waitgroup we are done when exiting
	defer func() {
		if wg != nil {
			wg.Done()
		}
	}()

	// Query the performances
	perfres, err := methods.QueryPerf(ctx, r, queryperf)

	// Check the result
	if err != nil {
		log.Printf("vcenter %s thread %d: could not request perfs - %s\n", vcName, id, err)
		return
	}

	// Get the result
	returncount := len(perfres.Returnval)
	if returncount == 0 {
		log.Printf("vcenter %s thread %d: no result returned by queries\n", vcName, id)
		return
	}
	log.Printf("vcenter %s thread %d: retuned %d metrics\n", vcName, id, returncount)

	// create an array to store vm to folder path resolution
	cache.Purge(vcName, "folders")

	// Parse results
	// no need to wait here because this is only processing (no connection to vcenter needed)
	totalvms := 0
	totalhosts := 0
	var metricProcessWaitGroup sync.WaitGroup
	metricProcessWaitGroup.Add(len(perfres.Returnval))
	for _, base := range perfres.Returnval {
		pem := base.(*types.PerfEntityMetric)
		switch pem.Entity.Type {
		case "VirtualMachine":
			totalvms++
		case "HostSystem":
			totalhosts++
		}
		go ProcessMetric(cache, pem, timeStamp, replacepoint, domain, vcName, channel, &metricProcessWaitGroup)
	}
	// log total object referenced
	log.Printf("vcenter %s thread %d: %d objects (%d vm and %d hosts)", vcName, id, totalhosts+totalvms, totalvms, totalhosts)

	// Check missing values in the aftermath
	if requestedcount > returncount {
		log.Printf("vcenter %s thread %d: returned count is lower that requested count", vcName, id)
		// check requested entities
		reqmorefs := make([]string, requestedcount)
		for i := 0; i < requestedcount; i++ {
			reqmorefs[i] = queryperf.QuerySpec[i].Entity.Value
		}
		// check missing entities
		missmorefs := []string{}
		for i := 0; i < requestedcount; i++ {
			found := false
			for j := 0; j < returncount; j++ {
				if perfres.Returnval[j].(*types.PerfEntityMetric).Entity.Value == queryperf.QuerySpec[i].Entity.Value {
					found = true
					break
				}
			}
			if !found {
				missmorefs = append(missmorefs, queryperf.QuerySpec[i].Entity.Value)
			}
		}
		// output missing metrics
		for i := 0; i < len(missmorefs); i++ {
			log.Printf("vcenter %s thread %d: missing metrics for %s", vcName, id, missmorefs[i])
		}
	}

	// wait for metric processing
	metricProcessWaitGroup.Wait()
}

// ProcessMetric : Process Metric to metric queue
func ProcessMetric(cache *Cache, pem *types.PerfEntityMetric, timeStamp int64, replacepoint bool, domain string, vcName string, channel *chan backend.Point, wg *sync.WaitGroup) {
	// tell the wait group that we are finished at exit
	defer func() {
		if wg != nil {
			wg.Done()
		}
	}()
	// name checks the name of the object
	name := cache.FindString(vcName, "names", pem.Entity.Value)
	name = strings.ToLower(strings.Replace(name, domain, "", -1))
	if replacepoint {
		name = strings.Replace(name, ".", "_", -1)
	}
	//find datastore
	datastore := cache.FindNames(vcName, "datastores", pem.Entity.Value)
	//find host and cluster
	vmhost, cluster := cache.FindHostAndCluster(vcName, pem.Entity.Value)
	vmhost = strings.Replace(vmhost, domain, "", -1)
	//find network
	network := cache.FindNames(vcName, "networks", pem.Entity.Value)
	//find resource pool path
	resourcepool := cache.FindName(vcName, "poolpaths", pem.Entity.Value)
	//find folder path
	paths := []string{}
	if strings.HasPrefix(pem.Entity.Value, "vm-") {
		current := cache.GetString(vcName, "parents", pem.Entity.Value)
		for {
			if current == nil {
				break
			}
			if !(strings.HasPrefix(*current, "folder-") || strings.HasPrefix(*current, "group-")) {
				break
			}
			foldername := cache.GetString(vcName, "names", *current)
			if foldername == nil {
				log.Printf("vcenter %s: folder name not found for %s", vcName, *current)
				break
			}
			if *foldername == "vm" {
				break
			}
			paths = append(paths, *foldername)
			current = cache.GetString(vcName, "parents", *current)
		}
	}
	utils.Reverse(paths)
	folderpath := strings.Join(paths, "/")
	//find tags
	vitags := cache.FindTags(vcName, pem.Entity.Value)
	if len(pem.Value) == 0 {
		log.Printf("vcenter %s: no values returned in query!", vcName)
	}
	objType := strings.ToLower(pem.Entity.Type)
	// replace point in vcname
	rvcname := vcName
	if replacepoint {
		rvcname = strings.Replace(rvcname, ".", "_", -1)
	}
	// prepare basic information's of point
	point := backend.Point{
		VCenter:      rvcname,
		ObjectType:   objType,
		ObjectName:   name,
		Datastore:    datastore,
		ESXi:         vmhost,
		Cluster:      cluster,
		Network:      network,
		ResourcePool: resourcepool,
		Folder:       folderpath,
		ViTags:       vitags,
		Timestamp:    timeStamp,
	}
	//send disk infos
	diskInfos := cache.GetDiskInfos(vcName, "disks", pem.Entity.Value)
	if diskInfos != nil {
		for _, diskInfo := range *diskInfos {
			// skip if no capacity
			if diskInfo.Capacity == 0 {
				continue
			}
			// format disk path
			diskPath := strings.Replace(diskInfo.DiskPath, "\\", "/", -1)
			// send free space
			point.Group = "guestdisk"
			point.Counter = "freespace"
			point.Instance = diskPath
			point.Rollup = "latest"
			point.Value = diskInfo.FreeSpace
			*channel <- point
			// send capacity
			point.Group = "guestdisk"
			point.Counter = "capacity"
			point.Instance = diskPath
			point.Rollup = "latest"
			point.Value = diskInfo.Capacity
			*channel <- point
			// send usage %
			point.Group = "guestdisk"
			point.Counter = "usage"
			point.Instance = diskPath
			point.Rollup = "latest"
			point.Value = int64(10000 * (1 - (float64(diskInfo.FreeSpace) / float64(diskInfo.Capacity))))
			*channel <- point
		}
	}
	// send numcpu infos
	numcpu := cache.GetInt32(vcName, "cpus", pem.Entity.Value)
	if numcpu != nil {
		point.Group = "cpu"
		point.Counter = "count"
		point.Instance = ""
		point.Rollup = "latest"
		point.Value = int64(*numcpu)
		*channel <- point
	}
	// send numcpu infos
	memorysizemb := cache.GetInt32(vcName, "mems", pem.Entity.Value)
	if memorysizemb != nil {
		point.Group = "mem"
		point.Counter = "sizemb"
		point.Instance = ""
		point.Rollup = "latest"
		point.Value = int64(*memorysizemb)
		*channel <- point
	}
	networks := cache.GetMorefs(vcName, "networks", pem.Entity.Value)
	if networks != nil {
		for _, netRef := range *networks {
			// send shaping infos
			shaping_input := cache.GetNetworkShapingInfo(vcName, "shaping_inputs", netRef.Value)
			if shaping_input != nil {
				netBack := point.Network
				point.Network = []string{cache.FindString(vcName, "names", netRef.Value)}
				point.Group = "net"
				point.Counter = "shaping_input"
				point.Instance = ""
				point.Rollup = "latest"
				point.Value = int64(shaping_input.AverageBandwidth.Value)
				*channel <- point
				point.Network = netBack
			}
			shaping_output := cache.GetNetworkShapingInfo(vcName, "shaping_outputs", netRef.Value)
			if shaping_output != nil {
				netBack := point.Network
				point.Network = []string{cache.FindString(vcName, "names", netRef.Value)}
				point.Group = "net"
				point.Counter = "shaping_output"
				point.Instance = ""
				point.Rollup = "latest"
				point.Value = int64(shaping_output.AverageBandwidth.Value)
				*channel <- point
				point.Network = netBack
			}
		}
	}
	for _, baseserie := range pem.Value {
		serie := baseserie.(*types.PerfMetricIntSeries)
		metricName := cache.FindMetricName(vcName, serie.Id.CounterId)
		metricName = strings.ToLower(metricName)
		metricparts := strings.Split(metricName, ".")
		point.Group, point.Counter, point.Rollup = metricparts[0], metricparts[1], metricparts[2]
		point.Instance = serie.Id.Instance
		point.Network = network
		if len(point.Instance) > 0 && point.Group == "datastore" {
			newDatastore := cache.GetString(vcName, "datastoreids", point.Instance)
			if newDatastore != nil {
				point.Datastore = []string{*newDatastore}
			} else {
				point.Datastore = []string{}
			}
		}
		// Only show relevant networks according to the instance
		if len(point.Instance) > 0 && point.Group == "net" {
			if instanceValue, err := strconv.Atoi(point.Instance); err == nil {
				devs := cache.GetDevices(vcName, "devices", pem.Entity.Value)
				if devs != nil {
					for _, dev := range devs.VirtualDevice {
						vdev := dev.GetVirtualDevice()
						if vdev.Key == int32(instanceValue) {
							if nic, ok := dev.(types.BaseVirtualEthernetCard); ok {
								backing := nic.GetVirtualEthernetCard().Backing
								switch b := backing.(type) {
								case *types.VirtualEthernetCardNetworkBackingInfo:
									point.Network = []string{cache.FindString(vcName, "names", b.Network.Value)}
								case *types.VirtualEthernetCardDistributedVirtualPortBackingInfo:
									point.Network = []string{cache.FindString(vcName, "names", b.Port.PortgroupKey)}
								}
							}
						}
					}
				}
			}
		}
		var value int64 = -1
		if len(serie.Value) == 0 {
			log.Printf("process: point %s for %s has no values", metricName, point.ObjectName)
			point.Value = value
			*channel <- point
			return
		}
		switch {
		case strings.HasSuffix(metricName, ".average"):
			value = utils.Average(serie.Value...)
		case strings.HasSuffix(metricName, ".maximum"):
			value = utils.Max(serie.Value...)
		case strings.HasSuffix(metricName, ".minimum"):
			value = utils.Min(serie.Value...)
		case strings.HasSuffix(metricName, ".latest"):
			value = serie.Value[len(serie.Value)-1]
		case strings.HasSuffix(metricName, ".summation"):
			value = utils.Sum(serie.Value...)
		}
		point.Value = value
		*channel <- point
	}
}
