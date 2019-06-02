package backend

import (
	"context"
	"encoding/json"
	"log"
	"reflect"
	"strings"

	elastic "gopkg.in/olivere/elastic.v5"
)

// CreateIndexIfNotExists not exists creates Elasticsearch Index if Not Exists
func CreateIndexIfNotExists(e *elastic.Client, index string) error {
	// Use the IndexExists service to check if a specified index exists.
	exists, err := e.IndexExists(index).Do(context.Background())
	if err != nil {
		log.Printf("elastic: unable to check if Index exists - %s\n", err)
		return err
	}

	if exists {
		return nil
	}

	// Create a new index.
	v := reflect.TypeOf(Point{})

	mapping := MapStr{
		"settings": MapStr{
			"number_of_shards":   1,
			"number_of_replicas": 1,
		},
		"mappings": MapStr{
			"doc": MapStr{
				"properties": MapStr{},
			},
		},
	}
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		tag := field.Tag.Get("elastic")
		if len(tag) == 0 {
			continue
		}
		tagfields := strings.Split(tag, ",")
		mapping["mappings"].(MapStr)["doc"].(MapStr)["properties"].(MapStr)[field.Name] = MapStr{}
		for _, tagfield := range tagfields {
			tagfieldValues := strings.Split(tagfield, ":")
			mapping["mappings"].(MapStr)["doc"].(MapStr)["properties"].(MapStr)[field.Name].(MapStr)[tagfieldValues[0]] = tagfieldValues[1]
		}
	}
	mappingJSON, err := json.Marshal(mapping)
	if err != nil {
		log.Printf("elastic: error on json marshal - %s\n", err)
		return err
	}

	_, err = e.CreateIndex(index).BodyString(string(mappingJSON)).Do(context.Background())
	if err != nil {
		log.Printf("elastic: error creating elastic index %s - %s\n", index, err)
		return err
	}
	log.Printf("elastic: index %s created\n", index)
	return nil
}
