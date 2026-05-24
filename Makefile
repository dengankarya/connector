.PHONY: run redis redis-stop redis-logs docker-build

run:
	go run http/*.go

docker-build:
	docker build -t connector .
