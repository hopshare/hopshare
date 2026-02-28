#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

POD_NAME="${POD_NAME:-hopshare}"
APP_IMAGE="${APP_IMAGE:-quay.io/hopshare/hopshare:nightly}"
APP_CONTAINER="${APP_CONTAINER:-hopshare-app}"
DB_CONTAINER="${DB_CONTAINER:-hopshare-db}"

APP_PORT="${APP_PORT:-8080}"
DB_PORT="${DB_PORT:-5432}"

DB_NAME="${DB_NAME:-hopshare}"
DB_USER="${DB_USER:-hopshare}"
DB_PASSWORD="${DB_PASSWORD:-hopshare}"
DB_DATA_DIR="${DB_DATA_DIR:-${REPO_ROOT}/deploy/data/postgres}"

if podman pod exists "${POD_NAME}"; then
	echo "Pod ${POD_NAME} already exists. Run deploy/scripts/stop.sh first."
	exit 1
fi

if podman container exists "${APP_CONTAINER}"; then
	echo "Container ${APP_CONTAINER} already exists. Remove it or run deploy/scripts/stop.sh."
	exit 1
fi

if podman container exists "${DB_CONTAINER}"; then
	echo "Container ${DB_CONTAINER} already exists. Remove it or run deploy/scripts/stop.sh."
	exit 1
fi

mkdir -p "${DB_DATA_DIR}"

echo "Pulling ${APP_IMAGE}..."
podman pull "${APP_IMAGE}" >/dev/null

cleanup_on_error() {
	echo "Startup failed. Cleaning up pod resources..."
	podman rm -f "${APP_CONTAINER}" >/dev/null 2>&1 || true
	podman rm -f "${DB_CONTAINER}" >/dev/null 2>&1 || true
	podman pod rm -f "${POD_NAME}" >/dev/null 2>&1 || true
}

trap cleanup_on_error ERR

echo "Creating pod ${POD_NAME}..."
podman pod create \
	--name "${POD_NAME}" \
	-p "${APP_PORT}:8080" \
	-p "${DB_PORT}:5432" >/dev/null

echo "Starting Postgres container ${DB_CONTAINER}..."
podman run -d \
	--name "${DB_CONTAINER}" \
	--pod "${POD_NAME}" \
	-e POSTGRES_DB="${DB_NAME}" \
	-e POSTGRES_USER="${DB_USER}" \
	-e POSTGRES_PASSWORD="${DB_PASSWORD}" \
	-v "${DB_DATA_DIR}:/var/lib/postgresql/data" \
	docker.io/library/postgres:17.7 >/dev/null

echo "Waiting for Postgres to become ready..."
for _ in $(seq 1 45); do
	if podman exec "${DB_CONTAINER}" pg_isready -U "${DB_USER}" -d "${DB_NAME}" >/dev/null 2>&1; then
		break
	fi
	sleep 1
done

if ! podman exec "${DB_CONTAINER}" pg_isready -U "${DB_USER}" -d "${DB_NAME}" >/dev/null 2>&1; then
	echo "Postgres did not become ready in time."
	exit 1
fi

DB_URL="postgres://${DB_USER}:${DB_PASSWORD}@127.0.0.1:5432/${DB_NAME}?sslmode=disable"

echo "Starting Hopshare container ${APP_CONTAINER}..."
podman run -d \
	--name "${APP_CONTAINER}" \
	--pod "${POD_NAME}" \
	-e HOPSHARE_ENV=production \
	-e HOPSHARE_ADDR=":8080" \
	-e HOPSHARE_DB_URL="${DB_URL}" \
	-e HOPSHARE_MAILGUN_API_KEY="foobar" \
	"${APP_IMAGE}" >/dev/null

trap - ERR

echo "Hopshare is available on http://localhost:${APP_PORT}"
echo "Pod: ${POD_NAME}"
echo "Containers: ${APP_CONTAINER}, ${DB_CONTAINER}"
echo "Postgres data dir: ${DB_DATA_DIR}"
