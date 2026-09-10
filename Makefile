#!make
ifeq ($(filter snapshot-%,$(MAKECMDGOALS)),)
include .env
export $(shell sed 's/=.*//' .env)
else
ifneq ($(filter-out snapshot-%,$(MAKECMDGOALS)),)
$(error Run snapshot targets separately from development targets)
endif
endif

.PHONY: db migrate run validator

db:
	docker-compose -p ${DB_CONTAINER_NAME}  -f dc-db.yml down
	$(shell mkdir -p ${REPACK_DIR})
	docker-compose -p ${DB_CONTAINER_NAME}  -f dc-db.yml up -d

local:
	docker-compose -p ${DB_CONTAINER_NAME}  -f dc-db.yml down
	$(shell mkdir -p ${REPACK_DIR})
	docker-compose -p ${DB_CONTAINER_NAME}  -f dc-db.yml up -d validator

remote:
	docker-compose -p ${DB_CONTAINER_NAME}  -f dc-db.yml down
	$(shell mkdir -p ${REPACK_DIR})
	docker-compose -p ${DB_CONTAINER_NAME}  -f dc-db.yml up -d database postgres

rebuild-mysql:
	docker-compose -p ${DB_CONTAINER_NAME} down
	docker volume rm ${DB_CONTAINER_NAME}_fpfssdb_data
	docker-compose -p ${DB_CONTAINER_NAME} -f dc-db.yml up -d
	sleep 10
	make migrate

rebuild-postgres:
	docker-compose -p ${DB_CONTAINER_NAME} down
	docker volume rm ${DB_CONTAINER_NAME}_fpfss_postgres_data
	docker-compose -p ${DB_CONTAINER_NAME} -f dc-db.yml up -d
	sleep 10
	make migrate

migrate:
	@if [ "${SUBMISSION_DB_ENGINE}" != "postgres" ]; then docker run --rm -v $(shell pwd)/migrations:/migrations --network host migrate/migrate -path=/migrations/ -database "mysql://${DB_USER}:${DB_PASSWORD}@tcp(${DB_IP}:${DB_PORT})/${DB_NAME}" up; fi
	docker run --rm -v $(shell pwd)/postgres_migrations:/migrations --network host migrate/migrate -path=/migrations/ -database "postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@${POSTGRES_HOST}:${POSTGRES_PORT}?sslmode=disable" up

migrate-to:
	docker run --rm -v $(shell pwd)/migrations:/migrations --network host migrate/migrate -path=/migrations/ -database "mysql://${DB_USER}:${DB_PASSWORD}@tcp(${DB_IP}:${DB_PORT})/${DB_NAME}" goto $(MIGRATION)

migrate-to-pgdb:
	docker run --rm -v $(shell pwd)/postgres_migrations:/migrations --network host migrate/migrate -path=/migrations/ -database "postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@${POSTGRES_HOST}:${POSTGRES_PORT}?sslmode=disable" goto $(MIGRATION)

validator:
	cd .. && cd Curation-Validation-Bot && python3.9 -m uvicorn validator-server:app --host 127.0.0.1 --port 8371 --workers 8

archive-indexer:
	cd .. && cd recursive-archive-indexer && python3.9 -m uvicorn main:app --host 127.0.0.1 --port 8372 --workers 8

dump-db:
	mkdir -p ./backups/db/
	docker exec ${DB_CONTAINER_NAME} /usr/bin/mariadb-dump -u root --password=${DB_ROOT_PASSWORD} ${DB_NAME} --compress --dump-date --verbose > ./backups/db/db-dump-${DB_NAME}-$(shell date -u +"%Y-%m-%d-%H-%M-%S").sql

restore-db:
	cat $(SQL_FILE) | pv | docker exec -i ${DB_CONTAINER_NAME} /usr/bin/mariadb -u root --password=${DB_ROOT_PASSWORD} ${DB_NAME}

run:
	export GIT_COMMIT=$(shell git rev-list -1 HEAD) && /usr/local/go/bin/go run ./main/*.go

dump-pgdb:
	mkdir -p ./backups/pgdb/
	docker exec -e PGPASSWORD=${POSTGRES_PASSWORD} ${POSTGRES_CONTAINER_NAME} pg_dump -U ${POSTGRES_USER} > ./backups/pgdb/pgdb-dump-${DB_NAME}-$(shell date -u +"%Y-%m-%d-%H-%M-%S").sql

build-docs:
	swag init --parseInternal --dir main,transport,types,constants

# Disposable production-snapshot analysis; never uses .env or integration fixtures.
.PHONY: snapshot-restore snapshot-status snapshot-audit snapshot-down snapshot-cache
snapshot-restore:
	bash scripts/snapshot-db/launch.sh restore
snapshot-status:
	bash scripts/snapshot-db/launch.sh status
snapshot-audit:
	bash scripts/snapshot-db/launch.sh audit
snapshot-down:
	bash scripts/snapshot-db/launch.sh down

snapshot-cache:
	bash scripts/snapshot-db/launch.sh cache

.PHONY: snapshot-search
snapshot-search:
	bash scripts/snapshot-db/launch.sh search

.PHONY: db-postgres snapshot-import
db-postgres:
	docker-compose -p ${DB_CONTAINER_NAME} -f dc-db.yml up -d postgres

snapshot-import:
	bash scripts/submission-import/launch.sh

.PHONY: snapshot-parity
snapshot-parity:
	bash scripts/submission-parity/launch.sh

.PHONY: snapshot-concurrency
snapshot-concurrency:
	bash scripts/submission-concurrency/run.sh
