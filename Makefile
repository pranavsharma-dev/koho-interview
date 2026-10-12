.PHONY: db run test

db:
	docker compose up -d --wait
	docker compose exec -T postgres psql -U postgres -d bank < schema.sql

run:
	go run ./cmd/server

test:
	go test ./... -race -count=1
