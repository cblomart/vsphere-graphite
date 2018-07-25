package vsphere

import (
	"bytes"
	"log"
	"strings"
	"sync"

	"github.com/cblomart/vsphere-graphite/utils"

	"github.com/vmware/govmomi/vim25/types"
)

// Cache will hold some informations in memory
type Cache map[string]interface{}

var lock = sync.RWMutex{}

// Gets an index
func index(vcenter, section, i string) string {
	var buffer bytes.Buffer
	_, err := buffer.WriteString(vcenter)
	if err != nil {
		return ""
	}
	_, err = buffer.WriteString("|")
	if err != nil {
		return ""
	}
	_, err = buffer.WriteString(section)
	if err != nil {
		return ""
	}
	_, err = buffer.WriteString("|")
	if err != nil {
		return ""
	}
	_, err = buffer.WriteString(i)
	if err != nil {
		return ""
	}
	return buffer.String()
}

// Add a value to the cache
func (c *Cache) Add(vcenter, section, i string, v interface{}) {
	if len(vcenter) == 0 || len(section) == 0 || len(i) == 0 || v == nil {
		return
	}
	switch typed := v.(type) {
	case string:
		c.add(vcenter, section, i, &typed)
	case []string:
		if len(typed) > 0 {
			c.add(vcenter, section, i, &typed)
		}
	case int32:
		c.add(vcenter, section, i, v)
	case types.ManagedObjectReference:
		c.add(vcenter, section, i, &(typed.Value))
	case types.ArrayOfManagedObjectReference:
		if len(typed.ManagedObjectReference) > 0 {
			c.add(vcenter, section, i, &(typed.ManagedObjectReference))
		}
	case types.ArrayOfTag:
		if len(typed.Tag) > 0 {
			c.add(vcenter, section, i, &(typed.Tag))
		}
	case types.ArrayOfGuestDiskInfo:
		if len(typed.GuestDiskInfo) > 0 {
			c.add(vcenter, section, i, &(typed.GuestDiskInfo))
		}
	default:
		log.Printf("Adding a unhandled type %T to cache for %s section %s and ref %s\n", v, vcenter, section, i)
	}
}

// add to the cache without type check
func (c *Cache) add(vcenter, section, i string, v interface{}) {
	lock.Lock()
	defer lock.Unlock()
	if v != nil {
		(*c)[index(vcenter, section, i)] = v
	}
}

// get a value from the cache
func (c *Cache) get(vcenter, section, i string) interface{} {
	lock.RLock()
	defer lock.RUnlock()
	if v, ok := (*c)[index(vcenter, section, i)]; ok {
		return v
	}
	return nil
}

// GetString gets a string from cache
func (c *Cache) GetString(vcenter, section, i string) *string {
	if v, ok := c.get(vcenter, section, i).(*string); ok {
		return v
	}
	return nil
}

// GetStrings gets an array of strings from cache
func (c *Cache) GetStrings(vcenter, section, i string) *[]string {
	if v, ok := c.get(vcenter, section, i).(*[]string); ok {
		return v
	}
	return nil
}

// GetInt32 get an int32 from cache
func (c *Cache) GetInt32(vcenter, section, i string) *int32 {
	if v, ok := c.get(vcenter, section, i).(int32); ok {
		return &v
	}
	return nil
}

// GetMoref gets a managed object reference from cache
func (c *Cache) GetMoref(vcenter, section, i string) *types.ManagedObjectReference {
	if v, ok := c.get(vcenter, section, i).(*types.ManagedObjectReference); ok {
		return v
	}
	return nil
}

// GetMorefs gets an array of managed references from cache
func (c *Cache) GetMorefs(vcenter, section, i string) *[]types.ManagedObjectReference {
	if v, ok := c.get(vcenter, section, i).(*[]types.ManagedObjectReference); ok {
		return v
	}
	return nil
}

// GetTags gets an array of vsphere tags from cache
func (c *Cache) GetTags(vcenter, section, i string) *[]types.Tag {
	if v, ok := c.get(vcenter, section, i).(*[]types.Tag); ok {
		return v
	}
	return nil
}

// GetDiskInfos gets an array of diskinfos from cache
func (c *Cache) GetDiskInfos(vcenter, section, i string) *[]types.GuestDiskInfo {
	if v, ok := c.get(vcenter, section, i).(*[]types.GuestDiskInfo); ok {
		return v
	}
	return nil
}

// Clean cache of unknows references
func (c *Cache) Clean(vcenter string, section string, refs []string) {
	lock.Lock()
	defer lock.Unlock()
	for e := range *c {
		// get back index parts
		m := strings.Split(e, "|")
		// check that we have tree parts or delete
		if len(m) != 3 {
			delete(*c, e)
		}
		// check vcenter
		if m[0] != vcenter || m[1] != section {
			continue
		}
		// find the value in the range
		found := false
		for _, ref := range refs {
			if m[2] == ref {
				found = true
				break
			}
		}
		// remove if not found
		if !found {
			log.Printf("removing %s from cache\n", e)
			delete(*c, e)
		}
	}
}

// CleanAll cleans all sections of unknown references
// poolpaths and metrics are ignored as they will be cleaned real time
func (c *Cache) CleanAll(vcenter string, refs []string) {
	lock.Lock()
	defer lock.Unlock()
	for e := range *c {
		// get back index parts
		m := strings.Split(e, "|")
		// check that we have tree parts or delete
		if len(m) != 3 {
			delete(*c, e)
		}
		// check vcenter and ignored sections
		if m[0] != vcenter || m[1] == "metrics" || m[1] == "poolpaths" {
			continue
		}
		// find the value in the range
		found := false
		for _, ref := range refs {
			if m[2] == ref {
				found = true
				break
			}
		}
		// remove if not found
		if !found {
			log.Printf("removing %s from cache\n", e)
			delete(*c, e)
		}
	}
}

// Purge purges a section of the cache
func (c *Cache) Purge(vcenter, section string) {
	lock.Lock()
	defer lock.Unlock()
	for e := range *c {
		// get back index parts
		m := strings.Split(e, "|")
		// check that we have tree parts
		if len(m) != 3 {
			continue
		}
		// check vcenter and ignored sections
		if m[0] != vcenter || m[1] != section {
			continue
		}
		delete(*c, e)
	}
}

// lookup items in the cache
func (c *Cache) lookup(vcenter, section string) *map[string]interface{} {
	lock.RLock()
	defer lock.RUnlock()
	result := make(map[string]interface{})
	for e := range *c {
		// get back index parts
		m := strings.Split(e, "|")
		// check that we have tree parts
		if len(m) != 3 {
			continue
		}
		// check vcenter and ignored sections
		if m[0] != vcenter || m[1] != section {
			continue
		}
		result[m[2]] = (*c)[e]
	}
	return &result
}

// LookupString looks for items in the cache of type string
func (c *Cache) LookupString(vcenter, section string) *map[string]*string {
	result := make(map[string]*string)
	for key, val := range *c.lookup(vcenter, section) {
		if typed, ok := val.(*string); ok {
			result[key] = typed
		}
	}
	return &result
}

// LookupMorefs looks for items in the cache of type Morefs
func (c *Cache) LookupMorefs(vcenter, section string) *map[string]*[]types.ManagedObjectReference {
	result := make(map[string]*[]types.ManagedObjectReference)
	for key, val := range *c.lookup(vcenter, section) {
		if typed, ok := val.(*[]types.ManagedObjectReference); ok {
			result[key] = typed
		}
	}
	return &result
}

// FindHostAndCluster finds host and cluster of a host or a vm
func (c *Cache) FindHostAndCluster(vcenter, moref string) (string, string) {
	// get host
	if strings.HasPrefix(moref, "vm-") {
		// find host of the vm
		host := c.GetString(vcenter, "hosts", moref)
		if host == nil {
			log.Printf("VM %s has no host.\n", moref)
			return "", ""
		}
		moref = *host
	}
	// find hostname
	hostnameptr := cache.GetString(vcenter, "names", moref)
	hostname := ""
	if hostnameptr != nil {
		hostname = *hostnameptr
	}
	// find cluster
	cluster := cache.GetString(vcenter, "parents", moref)
	if cluster == nil {
		log.Printf("Host %s has no parents.\n", moref)
		return hostname, ""
	}
	if strings.HasPrefix(*cluster, "domain-s") {
		//ignore standalone hosts
		return hostname, ""
	}
	if !strings.HasPrefix(*cluster, "domain-c") {
		log.Printf("Host %s has no suitable parent %s.", moref, *cluster)
		return hostname, ""
	}
	clusternameptr := cache.GetString(vcenter, "names", *cluster)
	if clusternameptr == nil {
		return hostname, ""
	}
	return hostname, *clusternameptr
}

// FindString finds and return a string
func (c *Cache) FindString(vcenter, section, moref string) string {
	ptr := cache.GetString(vcenter, section, moref)
	if ptr == nil {
		return ""
	}
	return *ptr
}

// FindName finds an object in cache and resolves its name
func (c *Cache) FindName(vcenter, section, moref string) string {
	ptr := cache.GetString(vcenter, section, moref)
	if ptr == nil {
		return ""
	}
	return cache.FindString(vcenter, "names", *ptr)
}

// FindNames finds objects in cache and resolves their names
func (c *Cache) FindNames(vcenter, section, moref string) []string {
	names := []string{}
	ptr := cache.GetMorefs(vcenter, section, moref)
	if ptr == nil {
		return names
	}
	if len(*ptr) == 0 {
		return names
	}
	for _, mor := range *ptr {
		nptr := cache.GetString(vcenter, "names", mor.Value)
		if nptr == nil {
			continue
		}
		if len(*nptr) == 0 {
			continue
		}
		names = append(names, *nptr)
	}
	return names
}

// FindTags finds objects in cachee and create a tag array
func (c *Cache) FindTags(vcenter, moref string) []string {
	tags := []string{}
	ptr := cache.GetTags(vcenter, "tags", moref)
	if ptr == nil {
		return tags
	}
	if len(*ptr) == 0 {
		return tags
	}
	for _, tag := range *ptr {
		tags = append(tags, tag.Key)
	}
	return tags
}

// FindMetricName find metricname from cache
func (c *Cache) FindMetricName(vcenter string, id int32) string {
	ptr := cache.GetString(vcenter, "metrics", utils.ValToString(id, "", true))
	if ptr == nil {
		return ""
	}
	return *ptr
}
