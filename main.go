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

type stationIDFlag []string

func (s *stationIDFlag) String() string {
	return strings.Join(*s, ", ")
}

func (s *stationIDFlag) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func main() {
	var stationIDs stationIDFlag
	flag.Var(&stationIDs, "id", "station ID to query for, needs to be passed at least 1 time")
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
	if len(stationIDs) == 0 {
		log.Fatalln("At least one station ID is required to be able to query the Trafikinfo API")
	}

	stationFilters := make([]trafikinfo.Filter, 0, len(stationIDs))
	for _, station := range stationIDs {
		stationFilters = append(stationFilters, trafikinfo.Equal("Id", station))
	}

	req, err := trafikinfo.NewRequest().
		APIKey(*apiToken).
		Query(
			trafikinfo.NewQuery(
				trafikinfo.WeatherStation,
				1.0,
			).Filter(
				trafikinfo.Or(stationFilters...),
			).Include(
				"Active", "Id", "Name", "Measurement", "RoadNumberNumeric",
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
			item.name, item.id, item.roadNum, m,
		)
		stations[item.id] = station
	}

	if len(stations) != len(stationIDs) {
		notfound := []string{}
		for _, id := range stationIDs {
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
	id      string
	name    string
	tempC   float64
	rhPct   float64
	precip  float64
	roadNum int
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
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("invalid credentials")
	}

	if resp.StatusCode == http.StatusBadRequest {
		type errmsg struct {
			Response struct {
				Result []struct {
					Error struct {
						Message string `json:"MESSAGE"`
					} `json:"ERROR"`
				} `json:"RESULT"`
			} `json:"RESPONSE"`
		}
		var e errmsg
		d := json.NewDecoder(resp.Body)
		err := d.Decode(&e)
		if err != nil {
			return nil, fmt.Errorf("failed to decode API error response: %w", err)
		}
		return nil, fmt.Errorf("invalid request: %s", e.Response.Result[0].Error.Message)
	}

	if resp.StatusCode != 200 {
		io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("got status code: %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}

	type weatherstationResp struct {
		Response struct {
			Result []struct {
				WeatherStation []trafikinfo.WeatherStation1Dot0 `json:"WeatherStation"`
			} `json:"RESULT"`
		} `json:"RESPONSE"`
	}

	d := json.NewDecoder(resp.Body)
	var wr weatherstationResp
	err = d.Decode(&wr)
	if err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if numRes := len(wr.Response.Result); numRes != 1 {
		return nil, fmt.Errorf("expected 1 query result, got %d", numRes)
	}

	res := []data{}
	for _, station := range wr.Response.Result[0].WeatherStation {
		if station.Active != nil && !*station.Active {
			continue
		}
		precip := 0.0
		if data := station.Measurement.Precipitation.Amount; data != nil {
			precip = *data
		}
		res = append(res, data{
			id:      *station.ID,
			name:    *station.Name,
			tempC:   *station.Measurement.Air.Temperature,
			rhPct:   *station.Measurement.Air.RelativeHumidity,
			precip:  precip,
			roadNum: *station.RoadNumber,
		})
	}

	return res, nil
}
