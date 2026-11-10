package main

import (
	caddycmd "github.com/caddyserver/caddy/v2/cmd"

	_ "github.com/mliezun/caddy-snake"
	_ "github.com/mliezun/caddy-snake/apps"
)

func main() {
	caddycmd.Main()
}
