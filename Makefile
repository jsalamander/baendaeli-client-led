.PHONY: run preview debug

run:
	go run ./...

DEBUG_BIN ?= baendaeli-client-led-debug
EMOTION ?= happy
OUTPUT ?= previews/$(EMOTION).gif

preview:
	go run . -preview "$(EMOTION)" -output "$(OUTPUT)"

debug:
	APP_ENV=local go build -o "$(DEBUG_BIN)" .
	@echo "Built debug binary: $(DEBUG_BIN)"
	@echo "Example: ./$(DEBUG_BIN) --manual"
