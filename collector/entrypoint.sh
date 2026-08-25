#!/bin/sh
set -eu

find /data/profiles -type d -exec chmod 700 '{}' ';'
find /data/profiles -type f -exec chmod 600 '{}' ';'
exec node dist/manager.js
