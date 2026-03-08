# Kneejerk

Kneejerk is a pentesting command line tool for scanning environment variables and other information from React websites.

## Features
* Scans JavaScript files of a provided URL for environment variables.
* Performs API endpoint enumeration throughout React codebase
* Outputs found env variables and API endpoints to the console or to a specified file.

## Installation

### Homebrew (macOS/Linux)

```sh
brew install MillerMedia/tap/kneejerk
```

### Go Install

```sh
go install -v github.com/MillerMedia/kneejerk/cmd/kneejerk@latest
```

## Usage

#### Single URL
```bash
kneejerk -u https://www.example.com -o output.txt
```

#### Using with [nuclei](https://github.com/projectdiscovery/nuclei)
```bash
nuclei -u https://www.example.com | kneejerk
```

#### Chained with other [Project Discovery](https://github.com/projectdiscovery) tools
```bash
subfinder -d example.com | httpx | nuclei | kneejerk
```

#### Example Output
```angular2html
[kneejerk] [js] [info] https://app.example.com/static/js/2.chunk.js [NODE_ENV:"production"]
[kneejerk] [js] [info] https://app.example.com/static/js/2.chunk.js [REACT_APP_BUILD:"Production"]
[kneejerk] [js] [high] https://app.example.com/static/js/2.chunk.js [REACT_APP_SECRET:"SECRET"]
[kneejerk] [js] [high] https://app.example.com/static/js/2.chunk.js [REACT_APP_KEY:"KEY"]
[kneejerk] [js] [info] https://app.example.com/static/js/2.chunk.js [REACT_APP_API_HOST:"https://app.example.com"]
[kneejerk] [js] [info] https://app.example.com/static/js/2.chunk.js [REACT_APP_WEB_HOST:"WEB_HOST"]
[kneejerk] [js] [info] https://app.example.com/static/js/2.chunk.js [REACT_APP_VERSION:"VERSION"]
[kneejerk] [js] [high] https://app.example.com/static/js/2.chunk.js [REACT_APP_NOT_SECRET_CODE:"NOT_SECRET_CODE"]
[kneejerk] [js] [medium] https://app.example.com/static/js/2.chunk.js [REACT_APP_CLIENT_DATA_BUCKET_NAME:"client-bucket"]
[kneejerk] [js] [medium] https://app.example.com/static/js/2.chunk.js [REACT_APP_REGION:"us-east-2"]
```

#### Example Output w/ nuclei

```bash
[tech-detect:react] [http] [info] https://app.example.com
[kneejerk] [js] [info] https://app.example.com/static/js/2.chunk.js [NODE_ENV:"production"]
[kneejerk] [js] [info] https://app.example.com/static/js/2.chunk.js [REACT_APP_BUILD:"Production"]
[kneejerk] [js] [high] https://app.example.com/static/js/2.chunk.js [REACT_APP_SECRET:"SECRET"]
[kneejerk] [js] [high] https://app.example.com/static/js/2.chunk.js [REACT_APP_KEY:"KEY"]
[kneejerk] [js] [info] https://app.example.com/static/js/2.chunk.js [REACT_APP_API_HOST:"https://app.example.com"]
[kneejerk] [js] [info] https://app.example.com/static/js/2.chunk.js [REACT_APP_WEB_HOST:"WEB_HOST"]
[kneejerk] [js] [info] https://app.example.com/static/js/2.chunk.js [REACT_APP_VERSION:"VERSION"]
[kneejerk] [js] [high] https://app.example.com/static/js/2.chunk.js [REACT_APP_NOT_SECRET_CODE:"NOT_SECRET_CODE"]
[kneejerk] [js] [medium] https://app.example.com/static/js/2.chunk.js [REACT_APP_CLIENT_DATA_BUCKET_NAME:"client-bucket"]
[kneejerk] [js] [medium] https://app.example.com/static/js/2.chunk.js [REACT_APP_REGION:"us-east-2"]
[tech-detect:google-font-api] [http] [info] https://app.example.com
[tech-detect:nginx] [http] [info] https://app.example.com
```

#### Flags/Options
```angular2html
Kneejerk - A tool for scanning environment variables in .js files

optional arguments:
  -debug
        Print debugging statements
  -l string
        Path to a file containing a list of URLs to scan
  -o string
        Path to output file
  -u string
        URL of the website to scan
```

## Contributing

I welcome contributions from the community! If you have any suggestions, bug reports, or ideas for improvement, feel free to open an issue or submit a pull request.

## Support the project

If you find this project helpful and would like to support its development, please consider donating:  
  
[![Buy me a coffee](https://www.buymeacoffee.com/assets/img/custom_images/orange_img.png)](https://www.buymeacoffee.com/yOd1JU9MQe)

## License

This project is licensed under the MIT License.
