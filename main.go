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
	"time"

	"code.dny.dev/trafikinfo"
	"lib.hemtjan.st/transport/mqtt"
)

var (
	stationID = flag.String("id", "REQUIRED", "Weatherstation ID to retrieve data for")
	apiToken  = flag.String("token", "REQUIRED", "Trafikinfo API token")

	version = "unknown"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
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
	if *stationID == "REQUIRED" {
		log.Fatalln("A station ID is required to be able to query the Trafikinfo API")
	}

	req, err := trafikinfo.NewRequest().
		APIKey(*apiToken).
		Query(
			trafikinfo.NewQuery(
				trafikinfo.WeatherStation,
				1.0,
			).Filter(
				trafikinfo.Equal("Id", *stationID),
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
	time.Sleep(10 * time.Second)

	tempSensor := newTempSensor(data.name, m)
	rhSensor := newRHSensor(data.name, m)

	err = tempSensor.Feature("currentTemperature").Update(
		strconv.FormatFloat(data.tempC, 'f', 1, 32),
	)
	if err != nil {
		log.Printf("MQTT: %s\n", err)
	}

	err = rhSensor.Feature("currentRelativeHumidity").Update(
		strconv.FormatFloat(data.rhPct, 'f', 1, 32),
	)
	if err != nil {
		log.Printf("MQTT: %s\n", err)
	}

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
			err = tempSensor.Feature("currentTemperature").Update(
				strconv.FormatFloat(data.tempC, 'f', 1, 32),
			)
			if err != nil {
				log.Printf("MQTT: failed to publish temperature update: %s\n", err)
			}
			err = rhSensor.Feature("currentRelativeHumidity").Update(
				strconv.FormatFloat(data.rhPct, 'f', 1, 32),
			)
			if err != nil {
				log.Printf("MQTT: failed to publish relative humidity update: %s\n", err)
			}
		}
	}
	os.Exit(0)
}

type data struct {
	name  string
	tempC float64
	rhPct float64
}

type weatherstationResp struct {
	Response struct {
		Result []struct {
			WeatherStation []*struct {
				Name        string `json:"Name"`
				Measurement struct {
					Air struct {
						Temp             float64 `json:"Temp"`
						RelativeHumidity float64 `json:"RelativeHumidity"`
					} `json:"Air"`
				} `json:"Measurement"`
			} `json:"WeatherStation"`
		} `json:"RESULT"`
	} `json:"RESPONSE"`
}

func retrieve(ctx context.Context, client *http.Client, body []byte) (data, error) {
	httpReq, err := http.NewRequest(http.MethodPost, trafikinfo.Endpoint, bytes.NewBuffer(body))
	if err != nil {
		return data{}, err
	}

	httpReq.Header.Set("content-type", "text/xml")

	resp, err := client.Do(httpReq)
	if err != nil {
		return data{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return data{}, fmt.Errorf("invalid credentials")
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
			return data{}, fmt.Errorf("failed to decode API error response: %w", err)
		}
		return data{}, fmt.Errorf("invalid request: %s", e.Response.Result[0].Error.Message)
	}

	if resp.StatusCode != 200 {
		io.Copy(io.Discard, resp.Body)
		return data{}, fmt.Errorf("got status code: %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}

	d := json.NewDecoder(resp.Body)
	var wr weatherstationResp
	err = d.Decode(&wr)
	if err != nil {
		return data{}, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(wr.Response.Result) == 0 {
		return data{}, fmt.Errorf("station with ID: %s does not exist", *stationID)
	}

	return data{
		name:  wr.Response.Result[0].WeatherStation[0].Name,
		tempC: wr.Response.Result[0].WeatherStation[0].Measurement.Air.Temp,
		rhPct: wr.Response.Result[0].WeatherStation[0].Measurement.Air.RelativeHumidity,
	}, nil
}
