.PHONY: run preview

run:
	go run ./...

EMOTION ?= happy
OUTPUT ?= previews/$(EMOTION).gif

preview:
	go run . -preview "$(EMOTION)" -output "$(OUTPUT)"
