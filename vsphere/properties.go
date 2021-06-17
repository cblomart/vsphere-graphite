package vsphere

// MetricProperties are properties that are sent as metrics
var MetricProperties = []string{"numcpu", "memorysizemb", "disks", "shaping_input", "shaping_output"}

// Properties describes know relation to properties to related objects and properties
var Properties = map[string]map[string][]string{
	"datastore": {
		"Datastore":      {"name"},
		"VirtualMachine": {"datastore"},
	},
	"urls": {
		"Datastore": {"summary.url"},
	},
	"host": {
		"HostSystem":     {"name", "parent"},
		"VirtualMachine": {"name", "runtime.host"},
	},
	"cluster": {
		"ClusterComputeResource": {"name"},
	},
	"network": {
		"DistributedVirtualPortgroup": {"name", "config.defaultPortConfig.inShapingPolicy", "config.defaultPortConfig.outShapingPolicy"},
		"Network":                     {"name"},
		"VirtualMachine":              {"network", "config.hardware.device"},
	},
	"resourcepool": {
		"ResourcePool": {"name", "parent", "vm"},
	},
	"folder": {
		"Folder":         {"name", "parent"},
		"VirtualMachine": {"parent"},
	},
	"tags": {
		"VirtualMachine": {"tag"},
		"HostSystem":     {"tag"},
	},
	"numcpu": {
		"VirtualMachine": {"summary.config.numCpu"},
	},
	"memorysizemb": {
		"VirtualMachine": {"summary.config.memorySizeMB"},
	},
	"disks": {
		"VirtualMachine": {"guest.disk"},
	},
}

// PropertiesSections represent the mapping of attributes to sections in the cache
var PropertiesSections = map[string]string{
	"summary.url":                 "urls",
	"name":                        "names",
	"datastore":                   "datastores",
	"network":                     "networks",
	"runtime.host":                "hosts",
	"parent":                      "parents",
	"vm":                          "vms",
	"tag":                         "tags",
	"summary.config.numCpu":       "cpus",
	"summary.config.memorySizeMB": "memories",
	"guest.disk":                  "disks",
	"runtime.connectionState":     "connections",
	"runtime.powerState":          "powers",
	"config.defaultPortConfig.inShapingPolicy":  "shaping_inputs",
	"config.defaultPortConfig.outShapingPolicy": "shaping_outputs",
	"config.hardware.device":                    "devices",
}
