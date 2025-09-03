#!/usr/bin/env bash

# needed so environment variables are available for docker compose
[ ! -f '.devcontainer/.env' ] && cp '.env' '.devcontainer/.env' || true
