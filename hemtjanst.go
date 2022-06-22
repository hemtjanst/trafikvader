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

func newTempSensor(name string, tr device.Transport) client.Device {
	dev, _ := client.NewDevice(&device.Info{
		Topic:        fmt.Sprintf("sensor/temperature/%s", topicName(name)),
		Manufacturer: "trafikväder",
		Name:         fmt.Sprintf("Temperature (%s)", name),
		Type:         "temperatureSensor",
		Features: map[string]*feature.Info{
			"currentTemperature": {
				Min: -50,
			}},
	}, tr)

	return dev
}

func newRHSensor(name string, tr device.Transport) client.Device {
	dev, _ := client.NewDevice(&device.Info{
		Topic:        fmt.Sprintf("sensor/humidity/%s", topicName(name)),
		Manufacturer: "trafikväder",
		Name:         fmt.Sprintf("Relative Humidity (%s)", name),
		Type:         "humiditySensor",
		Features: map[string]*feature.Info{
			"currentRelativeHumidity": {}},
	}, tr)

	return dev
}
