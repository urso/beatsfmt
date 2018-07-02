beatsfmt is drop in replacement for goimports, adding support for adding license file headers

- file with license header contents can be passed via CLI flag `-license`
- If not license is found, the current directory and all parent directories up
	until $GOPATH are searched for a `.go_license_header` file. If present, the
	contents will be used as license header, otherwise no license header will be added.
