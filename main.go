package main

import (
	"bytes"
	"context"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"code.dny.dev/trafikinfo"
	wmp "code.dny.dev/trafikinfo/trv/weathermeasurepoint/v2"
	"lib.hemtjan.st/client"
	"lib.hemtjan.st/transport/mqtt"
)

var (
	version = "unknown"
	commit  = "unknown"
	date    = "unknown"
)

type stationNamesFlag []string

func (s *stationNamesFlag) String() string {
	return strings.Join(*s, ", ")
}

func (s *stationNamesFlag) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func main() {
	var stationNames stationNamesFlag
	flag.Var(&stationNames, "name", "station name to query for, needs to be passed at least 1 time")
	apiToken := flag.String("token", "REQUIRED", "Trafikinfo API token")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Parameters:\n\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\n")
		fmt.Fprintf(os.Stderr, "Version: %s, Commit: %s, Date: %s\n", version, commit, date)
		fmt.Fprintf(os.Stderr, "\n")
	}

	mcfg := mqtt.MustFlags(flag.String, flag.Bool)
	flag.Parse()

	if *apiToken == "REQUIRED" {
		log.Fatalln("A token is required to be able to query the Trafikinfo API")
	}
	if len(stationNames) == 0 {
		log.Fatalln("At least one station name is required to be able to query the Trafikinfo API")
	}

	stationFilters := make([]trafikinfo.Filter, 0, len(stationNames))
	for _, station := range stationNames {
		stationFilters = append(stationFilters, trafikinfo.Equal("Name", station))
	}

	req, err := trafikinfo.NewRequest().
		APIKey(*apiToken).
		Query(
			trafikinfo.NewQuery(wmp.ObjectType()).Filter(
				trafikinfo.Or(stationFilters...),
			).Include(
				"Id", "Name",
				"Observation.Air.Temperature.Value",
				"Observation.Air.RelativeHumidity.Value",
				"Observation.Aggregated30minutes.Precipitation.TotalWaterEquivalent.Value",
				"Observation.Sample",
			),
		).Build()
	if err != nil {
		log.Fatalf("invalid query: %v\n", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	data, err := retrieve(ctx, http.DefaultClient, req)
	if err != nil {
		log.Fatalf("failed to fetch data from API: %s\n", err)
	}
	log.Println("fetched initial data")

	m, err := mqtt.New(ctx, mcfg())
	if err != nil {
		log.Fatalln(err)
	}

	go func() {
		for {
			ok, err := m.Start()
			if err != nil {
				log.Printf("MQTT Error: %s", err)
			}
			if !ok && ctx.Err() == nil {
				log.Fatalln("MQTT: could not (re)connect")
			}
			time.Sleep(5 * time.Second)
			log.Printf("MQTT: reconnecting")
		}
	}()

	stations := map[string]client.Device{}
	for _, item := range data {
		station := newWeatherStation(
			item.name, item.id, m,
		)
		stations[item.id] = station
	}

	if len(stations) != len(stationNames) {
		notfound := []string{}
		for _, id := range stationNames {
			if _, ok := stations[id]; !ok {
				notfound = append(notfound, id)
			}
		}
		log.Printf("Station IDs %s could not be found\n", strings.Join(notfound, ", "))
	}

	update(data, stations)
	log.Println("MQTT: published initial sensor data")

loop:
	for {
		select {
		case <-ctx.Done():
			log.Println("Received shutdown signal, terminating")
			break loop
		// Publish after every interval has elapsed
		case <-time.After(time.Duration(10 * time.Minute)):
			data, err := retrieve(ctx, http.DefaultClient, req)
			if err != nil {
				log.Printf("failed to fetch data from API: %s\n", err)
				continue
			}
			update(data, stations)
		}
	}
	os.Exit(0)
}

func update(sensors []sensor, stations map[string]client.Device) {
	for _, item := range sensors {
		station, ok := stations[item.id]
		if !ok {
			continue
		}

		if item.tempC != nil {
			err := station.Feature("currentTemperature").Update(
				strconv.FormatFloat(*item.tempC, 'f', 1, 32),
			)
			if err != nil {
				log.Printf("MQTT: failed to publish temperature: %s\n", err)
			}
		}

		if item.rhPct != nil {
			err := station.Feature("currentRelativeHumidity").Update(
				strconv.FormatFloat(*item.rhPct, 'f', 1, 32),
			)
			if err != nil {
				log.Printf("MQTT: failed to publish relative humidity: %s\n", err)
			}
		}

		precip := 0.0
		if item.precip != nil {
			precip = *item.precip * 2
		}
		err := station.Feature("precipitation").Update(
			strconv.FormatFloat(precip, 'f', 1, 32),
		)
		if err != nil {
			log.Printf("MQTT: failed to publish precipitation: %s\n", err)
		}
	}
}

type sensor struct {
	id     string
	name   string
	tempC  *float64
	rhPct  *float64
	precip *float64
}

func retrieve(ctx context.Context, client *http.Client, body []byte) ([]sensor, error) {
	httpReq, err := http.NewRequest(http.MethodPost, trafikinfo.Endpoint, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("content-type", "text/xml")

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}

	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var wr wmp.Response
	if err := xml.Unmarshal(data, &wr); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if resp.StatusCode != http.StatusOK || wr.HasErrors() {
		return nil, fmt.Errorf("http code: %d, error: %s", resp.StatusCode, wr.ErrorMsg())
	}

	if numRes := len(wr.Results); numRes != 1 {
		return nil, fmt.Errorf("expected 1 query result, got %d", numRes)
	}

	sensors := []sensor{}
	for _, mp := range wr.Results[0].Data {
		// Don't bother updating if samples are old. This usually indicates the station is
		// malfunctioning or offline for maintenance
		if mp.Observation().Sample().Before(time.Now().Add(-1 * time.Hour)) {
			continue
		}

		sensors = append(sensors, sensor{
			id:     *mp.ID(),
			name:   *mp.Name(),
			tempC:  mp.Observation().Air().Temperature().Value(),
			rhPct:  mp.Observation().Air().RelativeHumidity().Value(),
			precip: mp.Observation().Aggregated30minutes().Precipitation().TotalWaterEquivalent().Value(),
		})
	}

	return sensors, nil
}
