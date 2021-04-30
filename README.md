# esbuild-webserver

A simple web-server that can be used as an alternative to Webpack's `dev-server`

## Compile

```
go build -o ~/go/bin/esbuild-webserver ./cmd/esbuild-webserver
```

## Usage

```bash
$ esbuild-webserver -h

Usage: esbuild-webserver --endpoint ENDPOINT [--listen LISTEN]

Options:
  --endpoint ENDPOINT, -e ENDPOINT
  --listen LISTEN, -l LISTEN [default: 127.0.0.1:8080]
  --help, -h             display this help and exit
```

## Example

```makefile
serve:
	esbuild-webserver \
		-e /api:proxy=http://127.0.0.1:8080 \
		-e /static:proxy=http://127.0.0.1:8080 \
		-e /:file=./dist/ \
		-e 404:404=./dist/index.html \
		-l 127.0.0.1:8000
```

