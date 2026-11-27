package main

import "GoWork/internal/gowork"

var appVersion = "dev"

func main() {
	gowork.AppVersion = appVersion
	gowork.Main()
}
