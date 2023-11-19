package main

import (
	"bytes"
	"context"
	"encoding/json"
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
			trafikinfo.NewQuery(
				trafikinfo.WeatherMeasurepoint,
				2.0,
			).Filter(
				trafikinfo.Or(stationFilters...),
			),
		).Build()
	if err != nil {
		log.Fatalln(err)
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

func update(data []data, stations map[string]client.Device) {
	for _, item := range data {
		station, ok := stations[item.id]
		if !ok {
			continue
		}

		err := station.Feature("currentTemperature").Update(
			strconv.FormatFloat(item.tempC, 'f', 1, 32),
		)
		if err != nil {
			log.Printf("MQTT: failed to publish temperature: %s\n", err)
		}

		err = station.Feature("currentRelativeHumidity").Update(
			strconv.FormatFloat(item.rhPct, 'f', 1, 32),
		)
		if err != nil {
			log.Printf("MQTT: failed to publish relative humidity: %s\n", err)
		}

		err = station.Feature("precipitation").Update(
			strconv.FormatFloat(item.precip, 'f', 1, 32),
		)
		if err != nil {
			log.Printf("MQTT: failed to publish precipitation: %s\n", err)
		}
	}
}

type data struct {
	id     string
	name   string
	tempC  float64
	rhPct  float64
	precip float64
}

func retrieve(ctx context.Context, client *http.Client, body []byte) ([]data, error) {
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

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("invalid credentials")
	}

	if resp.StatusCode == http.StatusBadRequest {
		var e trafikinfo.APIError
		d := json.NewDecoder(resp.Body)
		err := d.Decode(&e)
		if err != nil {
			return nil, fmt.Errorf("failed to decode API error response: %w", err)
		}
		return nil, fmt.Errorf("invalid request: %s", e.Response.Result[0].Error.Message)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("got status code: %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}

	type mpResp struct {
		Response struct {
			Result []struct {
				Measurepoints []trafikinfo.WeatherMeasurepoint2Dot0 `json:"WeatherMeasurepoint"`
			} `json:"RESULT"`
		} `json:"RESPONSE"`
	}

	d := json.NewDecoder(resp.Body)
	var wr mpResp
	err = d.Decode(&wr)
	if err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if numRes := len(wr.Response.Result); numRes != 1 {
		return nil, fmt.Errorf("expected 1 query result, got %d", numRes)
	}

	res := []data{}
	for _, mp := range wr.Response.Result[0].Measurepoints {
		// Don't bother updating if samples are old. This usually indicates the station is
		// malfunctioning or offline for maintenance
		if mp.Observation.Sample.Before(time.Now().Add(-1 * time.Hour)) {
			continue
		}
		precip := 0.0
		if data := mp.Observation.Aggregated30Minutes.Precipitation.TotalWaterEquivalent.Value; data != nil {
			precip = *data * 2 // 2*30min to get back to mm/hr
		}
		res = append(res, data{
			id:     *mp.ID,
			name:   *mp.Name,
			tempC:  *mp.Observation.Air.Temperature.Value,
			rhPct:  *mp.Observation.Air.RelativeHumidity.Value,
			precip: precip,
		})
	}

	return res, nil
}
