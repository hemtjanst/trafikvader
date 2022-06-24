package main

import (
	"fmt"
	"strings"

	"lib.hemtjan.st/client"
	"lib.hemtjan.st/device"
	"lib.hemtjan.st/feature"
)

func topicName(name string) string {
	return strings.ReplaceAll(
		strings.ReplaceAll(
			strings.ReplaceAll(
				strings.ToLower(name),
				"å", "ao"),
			"ä", "ae"),
		"ö", "oe")
}

func newWeatherStation(name, id string, tr device.Transport) client.Device {
	dev, _ := client.NewDevice(&device.Info{
		Topic:        fmt.Sprintf("sensor/environment/%s", topicName(name)),
		Manufacturer: "trafikväder",
		Name:         fmt.Sprintf("%s (%s)", name, id),
		Type:         "weatherStation",
		Features: map[string]*feature.Info{
			"currentTemperature": {
				Min: -50,
			},
			"currentRelativeHumidity": {},
			"precipitation":           {},
		},
	}, tr)

	return dev
}
