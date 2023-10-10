package main

import (
	"fmt"

	"lib.hemtjan.st/client"
	"lib.hemtjan.st/device"
	"lib.hemtjan.st/feature"
)

func newWeatherStation(name, id string, tr device.Transport) client.Device {
	dev, _ := client.NewDevice(&device.Info{
		Topic:        fmt.Sprintf("sensor/environment/%s", id),
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
