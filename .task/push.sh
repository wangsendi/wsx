#!/usr/bin/env bash
git add .
git commit -m "init websocket"
git push origin main

git tag v0.0.0-2510011
git push origin v0.0.0-2510011
