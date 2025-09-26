package main

import (
	caddycmd "github.com/caddyserver/caddy/v2/cmd"

	_ "github.com/mliezun/caddy-snake"
)

func main() {
	caddycmd.Main()
}
