#!make
include absolute.env
export $(shell sed 's/=.*//' absolute.env)

.PHONY: db migrate run

rebuild-db:
	docker compose -p ${DB_CONTAINER_NAME} down -v
	docker compose -p ${DB_CONTAINER_NAME} -f dc-db.yml up -d

migrate:
	docker run --rm -v ${MIGRATION_PATH}/migrations:/migrations --network host migrate/migrate -path=/migrations/ -database "mysql://${DB_USER}:${DB_PASSWORD}@tcp(${DB_IP}:${DB_PORT})/${DB_NAME}" up
	docker run --rm -v ${MIGRATION_PATH}/postgres_migrations:/migrations --network host migrate/migrate -path=/migrations/ -database "postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@${POSTGRES_HOST}:${POSTGRES_PORT}?sslmode=disable" up
