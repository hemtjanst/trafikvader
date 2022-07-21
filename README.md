<h1 align="center">
🚦 Trafikväder ☀️
</h1>
<h4 align="center">A Hemtjänst sensor using the <a href="https://api.trafikinfo.trafikverket.se/">Trafikinfo API</a> to retrieve weather data</h4>
<p align="center">
    <a href="https://github.com/hemtjanst/trafikvader/releases"><img src="https://img.shields.io/github/release/hemtjanst/trafikvader.svg" alt="Release"></a>
    <a href="LICENSE"><img src="https://img.shields.io/github/license/hemtjanst/trafikvader" alt="License: Apache-2"></a>
</p>

The trafikväder daemon exposes air temperature and relative humidity data from
a weather station as Hemtjänst sensors.

## Usage

```
Usage of trafikvader:

Parameters:

  -id value
    	station ID to query for, needs to be passed at least 1 time
[..]
  -token string
    	Trafikinfo API token (default "REQUIRED")
[..]
```
