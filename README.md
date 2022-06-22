<h1 align="center">
🚦 Trafikväder ☀️
</h1>
<h4 align="center">A Hemtjänst sensor using the <a href="https://api.trafikinfo.trafikverket.se/">Trafikinfo API</a> to retrieve weather data</h4>
<p align="center">
    <a href="https://github.com/hemtjanst/trafikvader/actions/workflows/release.yml"><img src="https://img.shields.io/github/release/hemtjanst/kraft.svg" alt="Release"></a>
    <a href="LICENSE"><img src="https://img.shields.io/github/license/daenney/trafikinfo" alt="License: MIT"></a>
</p>

The trafikväder daemon exposes air temperature and relative humidity data from
a weather station as Hemtjänst sensors.

## Usage

```
Usage of trafikvader:

Parameters:

  -id string
    	Weatherstation ID to retrieve data for (default "REQUIRED")
[..]
  -token string
    	Trafikinfo API token (default "REQUIRED")
[..]
```
