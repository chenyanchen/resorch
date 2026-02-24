package main

func main() {
	cfg, err := loadConfigFromFlag()
	must(err)

	container, err := resolveResources(cfg)
	must(err)

	must(runServer(container))
}
