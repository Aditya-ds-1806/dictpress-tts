package main

import (
	"dictpress-tts/internal/logger"
	"reflect"
)

func printConfig(cfg any) {
	v := reflect.ValueOf(cfg)

	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	logger.Logger.Println()
	logger.Logger.Printf("[%s]", reflect.TypeOf(cfg).Name())

	for i := range v.NumField() {
		field := v.Type().Field(i)
		value := v.Field(i).Elem()
		logger.Logger.Printf("%s: %v\n", field.Name, value)
	}

	logger.Logger.Println()
}

func ptr[T any](v T) *T {
	return &v
}
