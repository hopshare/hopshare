#!/usr/bin/env bash
set -euo pipefail

POD_NAME="${POD_NAME:-hopshare}"
APP_CONTAINER="${APP_CONTAINER:-hopshare-app}"
DB_CONTAINER="${DB_CONTAINER:-hopshare-db}"

if podman container exists "${APP_CONTAINER}"; then
	echo "Stopping ${APP_CONTAINER}..."
	podman rm -f "${APP_CONTAINER}" >/dev/null
fi

if podman container exists "${DB_CONTAINER}"; then
	echo "Stopping ${DB_CONTAINER}..."
	podman rm -f "${DB_CONTAINER}" >/dev/null
fi

if podman pod exists "${POD_NAME}"; then
	echo "Removing pod ${POD_NAME}..."
	podman pod rm -f "${POD_NAME}" >/dev/null
fi

echo "Stopped and removed Hopshare pod resources."
