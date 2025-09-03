# Devcontainer README

The devcontainer uses Docker to create a development environment for the project. You need Visual Studio Code to use this.

To get started, first follow setting up the environment in the main README, then:

1. Launch the devcontainer
2. Modifythe .env file by finding and updating the following variables:
```shell
DB_IP=10.50.0.2
POSTGRES_HOST=10.50.0.3
```
3. Migrate the DB by running these commands in the terminal:
```shell
export $(grep -v '^#' .env | xargs)
migrate -path=migrations -database "mysql://$DB_USER:$DB_PASSWORD@tcp($DB_IP)/$DB_NAME" up
migrate -path=postgres_migrations -database "postgres://$POSTGRES_USER:$POSTGRES_PASSWORD@$POSTGRES_HOST?sslmode=disable" up
```
4. Start the application with live-reloading:
```shell
GIN_PORT=8730 GIT_COMMIT=deadbeef gin --build ./main/ run ./main/main.go
```