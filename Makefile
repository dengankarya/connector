.PHONY: run redis redis-stop redis-logs docker-build docs generate-client migrate-new cf-tunel

run:
	go run http/*.go

docker-build:
	docker build -t connector .

docs:
	swag fmt -g http/main.go
	swag init -g http/main.go --parseDependency --output docs

# Usage: make generate-client output=../path/to/frontend/src/api
# Defaults to ../tokokarya/src/providers/ConnectorAPI if output is not specified.
output ?= ../tokokarya/src/providers/ConnectorAPI

# Usage: make migrate-new name=add_user_table
migrate-new:
	@if [ -z "$(name)" ]; then \
		read -p "Migration name: " name; \
		$(MAKE) migrate-new name=$$name; \
	else \
		NEXT=$$(printf "%06d" $$(( $$(ls db/migrations/*.up.sql 2>/dev/null | wc -l | tr -d ' ') + 1 ))); \
		touch db/migrations/$${NEXT}_$(name).up.sql db/migrations/$${NEXT}_$(name).down.sql; \
		echo "Created db/migrations/$${NEXT}_$(name).up.sql"; \
		echo "Created db/migrations/$${NEXT}_$(name).down.sql"; \
	fi

generate-client: docs
	@GENERATOR=$$(command -v openapi-generator-cli 2>/dev/null || command -v openapi-generator 2>/dev/null); \
	if [ -z "$$GENERATOR" ]; then \
		echo ""; \
		echo "openapi-generator is not installed. Install it first:"; \
		echo "  npm:  npm install -g @openapitools/openapi-generator-cli"; \
		echo "  brew: brew install openapi-generator"; \
		echo ""; \
		exit 1; \
	fi; \
	echo "Using $$GENERATOR"; \
	$$GENERATOR generate \
		-i docs/swagger.json \
		-g typescript-fetch \
		-o $(output) \
		--additional-properties=supportsES6=true,withSeparateModelsAndApi=true,modelPackage=models,apiPackage=api

# Cloudflare Tunnel
cf-tunnel:
	cloudflared tunnel --config .cloudflared/config.yml run connector

