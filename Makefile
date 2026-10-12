.PHONY: db run test

db:
	docker compose up -d --wait

run:
	go run ./cmd/server

test:
	go test ./... -race -count=1
