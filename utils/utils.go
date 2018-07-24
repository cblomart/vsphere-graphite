package utils

import (
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/vmware/govmomi/vim25/types"
)

var stdlog, errlog *log.Logger

// Init : set up loggers
func Init(standardLogs *log.Logger, errorLogs *log.Logger) {
	stdlog = standardLogs
	errlog = errorLogs
}

// Min : get the minimum of values
func Min(n ...int64) int64 {
	var min int64 = -1
	for _, i := range n {
		if i >= 0 {
			if min == -1 {
				min = i
			} else {
				if i < min {
					min = i
				}
			}
		}
	}
	return min
}

// Max : get the maximum of the values
func Max(n ...int64) int64 {
	var max int64 = -1
	for _, i := range n {
		if i >= 0 {
			if max == -1 {
				max = i
			} else {
				if i > max {
					max = i
				}
			}
		}
	}
	return max
}

// Sum : Sum the values
func Sum(n ...int64) int64 {
	var total int64
	for _, i := range n {
		if i > 0 {
			total += i
		}
	}
	return total
}

// Average : average the values
func Average(n ...int64) int64 {
	var total int64
	var count int64
	for _, i := range n {
		if i >= 0 {
			count++
			total += i
		}
	}
	favg := float64(total) / float64(count)
	return int64(math.Floor(favg + .5))
}

// MapObjRefs fills in object references into a map to another object reference
func MapObjRefs(property string, sourceVal *types.AnyType, dest map[string]*[]types.ManagedObjectReference, index string) error {
	mors, ok := (*sourceVal).(types.ArrayOfManagedObjectReference)
	if ok {
		if len(mors.ManagedObjectReference) > 0 {
			dest[index] = &mors.ManagedObjectReference
			return nil
		}
		return errors.New("Property " + property + " of " + index + " didn't contain any object references")
	}
	return errors.New("Property " + property + " of " + index + " was not a ManagedObjectReferences, it was " + fmt.Sprintf("%T", sourceVal))
}

// MapObjRef fills in object reference into a map to another object reference
func MapObjRef(property string, sourceVal *types.AnyType, dest map[string]*string, index string) error {
	mor, ok := (*sourceVal).(types.ManagedObjectReference)
	if ok {
		dest[index] = &mor.Value
		return nil
	}
	return errors.New("Property " + property + " of " + index + " was not a ManagedObjectReference, it was " + fmt.Sprintf("%T", sourceVal))
}

// MapObjInt32 fills in an int32 into a map to another object reference
func MapObjInt32(property string, sourceVal *types.AnyType, dest map[string]*int32, index string) error {
	val, ok := (*sourceVal).(int32)
	if ok {
		dest[index] = &val
		return nil
	}
	return errors.New("Property " + property + " of " + index + " was not an int32, it was " + fmt.Sprintf("%T", sourceVal))
}

// StringMaptoString converts a string map to csv or get the first value
func StringMaptoString(value []string, separator string, noarray bool) string {
	if len(value) == 0 {
		return ""
	}
	if noarray {
		return value[0]
	}
	return strings.Join(value, separator)
}

// IntMaptoString converts a int map to csv or get the first value
func IntMaptoString(value []int, separator string, noarray bool) string {
	if len(value) == 0 {
		return ""
	}
	if noarray {
		return strconv.Itoa(value[0])
	}
	var strval []string
	for _, i := range value {
		strval = append(strval, strconv.Itoa(i))
	}
	return strings.Join(strval, separator)
}

// Int32MaptoString converts a int32 map to csv or get the first value
func Int32MaptoString(value []int32, separator string, noarray bool) string {
	if len(value) == 0 {
		return ""
	}
	if noarray {
		return strconv.FormatInt(int64(value[0]), 10)
	}
	var strval []string
	for _, i := range value {
		strval = append(strval, strconv.FormatInt(int64(i), 10))
	}
	return strings.Join(strval, separator)
}

// Int64MaptoString converts a int64 map to csv or get the first value
func Int64MaptoString(value []int64, separator string, noarray bool) string {
	if len(value) == 0 {
		return ""
	}
	if noarray {
		return strconv.FormatInt(value[0], 10)
	}
	var strval []string
	for _, i := range value {
		strval = append(strval, strconv.FormatInt(i, 10))
	}
	return strings.Join(strval, separator)
}

// ValToString : try to convert interface to string. Separated by separator if slice
func ValToString(value interface{}, separator string, noarray bool) string {
	switch v := value.(type) {
	case string:
		return v
	case []string:
		return StringMaptoString(v, separator, noarray)
	case int:
		return strconv.Itoa(v)
	case []int:
		return IntMaptoString(v, separator, noarray)
	case int32:
		return strconv.FormatInt(int64(v), 10)
	case []int32:
		return Int32MaptoString(v, separator, noarray)
	case int64:
		return strconv.FormatInt(v, 10)
	case []int64:
		return Int64MaptoString(v, separator, noarray)
	default:
		return ""
	}
}

// Join map[int]string into a string
func Join(values map[int]string, separator string) string {
	var keys []int
	for k := range values {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	// create a map with the key parts in order
	var tmp []string
	for _, k := range keys {
		tmp = append(tmp, values[k])
	}
	return strings.Join(tmp, separator)
}

// MustAtoi converts a string to integer and return 0 i case of error
func MustAtoi(value string) int {
	i, err := strconv.Atoi(value)
	if err != nil {
		i = 0
	}
	return i
}

// FindHostAndCluster Find cluster for managed entity
func FindHostAndCluster(entity *string,
	vmToHost map[string]*string,
	morToParent map[string]*string,
	morToName map[string]*string) (*string, *string, error) {
	// get host
	var host *string
	var hostname *string
	var cluster *string
	if strings.HasPrefix(*entity, "vm-") {
		if vmhost, ok := vmToHost[*entity]; ok {
			host = vmhost
			if vmhostname, ok := morToName[*host]; ok {
				hostname = vmhostname
			}
		}
	} else if strings.HasPrefix(*entity, "host-") {
		host = entity
	}
	// check if a host was found
	if host == nil {
		return nil, nil, errors.New("No host found for " + *entity)
	}
	if parmor, ok := morToParent[*host]; ok {
		if strings.HasPrefix(*parmor, "domain-c") {
			cluster = morToName[*parmor]
		} else if !strings.HasPrefix(*parmor, "domain-s") {
			// ComputeRessource parent denotes a standalong host
			// Any other is weird
			return hostname, nil, errors.New("No suitable parent for host " + *host + " to determine cluster (" + *parmor + ")")
		}
	}
	return hostname, cluster, nil
}

// ConvertToKV converts a map[string]string to a csv with k=v pairs
func ConvertToKV(values map[string]string) string {
	var tmp []string
	for key, val := range values {
		if len(val) == 0 {
			continue
		}
		tmp = append(tmp, fmt.Sprintf("%s=%s", key, val))
	}
	return strings.Join(tmp, ",")
}

// Reverse reverses the values of an array
func Reverse(array []string) {
	for i := len(array)/2 - 1; i >= 0; i-- {
		opp := len(array) - 1 - i
		array[i], array[opp] = array[opp], array[i]
	}
}

// CleanStringMap removes unkonw keys from a map of string
func CleanStringMap(m map[string]*string, refs []string) {
	for mor := range m {
		found := false
		for _, refmor := range refs {
			if refmor == mor {
				found = true
				break
			}
		}
		if !found {
			stdlog.Println("removing mor from string map: " + mor)
			delete(m, mor)
		}
	}
}

// CleanMorefsMap removes unkonw keys from a map of manged object references
func CleanMorefsMap(m map[string]*[]types.ManagedObjectReference, refs []string) {
	for mor := range m {
		found := false
		for _, refmor := range refs {
			if refmor == mor {
				found = true
				break
			}
		}
		if !found {
			stdlog.Println("removing mor from moref map: " + mor)
			delete(m, mor)
		}
	}
}

// CleanInt32Map removes unkonw keys from a map of int32
func CleanInt32Map(m map[string]*int32, refs []string) {
	for mor := range m {
		found := false
		for _, refmor := range refs {
			if refmor == mor {
				found = true
				break
			}
		}
		if !found {
			stdlog.Println("removing mor from int map: " + mor)
			delete(m, mor)
		}
	}
}

// CleanTagsMap removes unkonw keys from a map of tag
func CleanTagsMap(m map[string]*[]types.Tag, refs []string) {
	for mor := range m {
		found := false
		for _, refmor := range refs {
			if refmor == mor {
				found = true
				break
			}
		}
		if !found {
			stdlog.Println("removing mor from tags map: " + mor)
			delete(m, mor)
		}
	}
}

// CleanDiskInfosMap removes unkonw keys from a map of tag
func CleanDiskInfosMap(m map[string]*[]types.GuestDiskInfo, refs []string) {
	for mor := range m {
		found := false
		for _, refmor := range refs {
			if refmor == mor {
				found = true
				break
			}
		}
		if !found {
			stdlog.Println("removing mor from disk info map: " + mor)
			delete(m, mor)
		}
	}
}

// CleanInt32StringMap removes unkonw keys from a map of tag
func CleanInt32StringMap(m map[int32]*string, refs []int32) {
	for e := range m {
		found := false
		for _, ref := range refs {
			if ref == e {
				found = true
				break
			}
		}
		if !found {
			stdlog.Println("removing mor from int32 string map: " + strconv.FormatInt(int64(e), 10))
			delete(m, e)
		}
	}
}
