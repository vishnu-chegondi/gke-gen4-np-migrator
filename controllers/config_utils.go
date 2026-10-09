package controllers

import (
	"context"
	"os"

	"k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func ReadConfigMapData(ctx context.Context, r client.Reader) (data map[string]string, err error) {
	var configMap v1.ConfigMap
	name := os.Getenv("CONFIG_MAP_NAME")
	namespace := os.Getenv("CONFIG_MAP_NAMESPACE")
	namespacedName := client.ObjectKey{Name: name, Namespace: namespace}
	err = r.Get(ctx, namespacedName, &configMap)
	return configMap.Data, err
}

func ReadConfigMapKey(ctx context.Context, r client.Reader, key string) (value string, err error) {
	var exists bool
	data, err := ReadConfigMapData(ctx, r)
	if err != nil {
		goto ExitFunction
	}
	value, exists = data[key]
	if exists {
		goto ExitFunction
	}

ExitFunction:
	return value, err
}
