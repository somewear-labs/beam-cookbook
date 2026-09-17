#!/usr/bin/env python3
"""Download Mapbox Dark tiles for Lacey, WA and package as MBTiles."""
import math
import sqlite3
import urllib.request
import os
import sys
from concurrent.futures import ThreadPoolExecutor, as_completed
from threading import Lock

MIN_LAT, MAX_LAT = 46.95, 47.10
MIN_LON, MAX_LON = -122.95, -122.70
MIN_ZOOM, MAX_ZOOM = 10, 16

TOKEN = os.environ.get("MAPBOX_TOKEN", "")
if not TOKEN:
    sys.exit("Set MAPBOX_TOKEN env var to your Mapbox public access token")
TILE_URL = f"https://api.mapbox.com/styles/v1/mapbox/dark-v11/tiles/256/{{z}}/{{x}}/{{y}}?access_token={TOKEN}"
OUTPUT = os.path.expanduser("~/Downloads/lacey_wa_mapbox_dark.mbtiles")
WORKERS = 12

def lon_to_x(lon, z):
    return int((lon + 180) / 360 * 2**z)

def lat_to_y(lat, z):
    r = math.radians(lat)
    return int((1 - math.log(math.tan(r) + 1 / math.cos(r)) / math.pi) / 2 * 2**z)

def tms_y(y, z):
    return (2**z - 1) - y

def fetch_tile(z, x, y):
    url = TILE_URL.format(z=z, x=x, y=y)
    req = urllib.request.Request(url, headers={
        "User-Agent": "beam-cookbook/1.0 tile-downloader"
    })
    with urllib.request.urlopen(req, timeout=20) as resp:
        return resp.read()

def main():
    tasks = []
    for z in range(MIN_ZOOM, MAX_ZOOM + 1):
        x0 = lon_to_x(MIN_LON, z); x1 = lon_to_x(MAX_LON, z)
        y0 = lat_to_y(MAX_LAT, z); y1 = lat_to_y(MIN_LAT, z)
        for x in range(x0, x1 + 1):
            for y in range(y0, y1 + 1):
                tasks.append((z, x, y))

    total = len(tasks)
    print(f"Downloading {total} tiles (zoom {MIN_ZOOM}-{MAX_ZOOM}) — Mapbox Dark, Lacey, WA...")

    if os.path.exists(OUTPUT):
        os.remove(OUTPUT)

    conn = sqlite3.connect(OUTPUT)
    c = conn.cursor()
    c.execute("CREATE TABLE metadata (name TEXT, value TEXT)")
    c.execute("""
        CREATE TABLE tiles (
            zoom_level INTEGER,
            tile_column INTEGER,
            tile_row INTEGER,
            tile_data BLOB
        )
    """)
    c.execute("CREATE UNIQUE INDEX tile_index ON tiles (zoom_level, tile_column, tile_row)")
    c.executemany("INSERT INTO metadata VALUES (?, ?)", [
        ("name",        "Mapbox Dark - Lacey, WA"),
        ("type",        "overlay"),
        ("version",     "1.1"),
        ("description", "Mapbox Dark style tiles - Lacey, Washington"),
        ("format",      "png"),
        ("bounds",      f"{MIN_LON},{MIN_LAT},{MAX_LON},{MAX_LAT}"),
        ("center",      "-122.8237,47.0343,14"),
        ("minzoom",     str(MIN_ZOOM)),
        ("maxzoom",     str(MAX_ZOOM)),
    ])
    conn.commit()

    count = 0
    errors = 0
    lock = Lock()
    batch = []
    BATCH_SIZE = 100

    def flush():
        c.executemany("INSERT OR REPLACE INTO tiles VALUES (?, ?, ?, ?)", batch)
        conn.commit()
        batch.clear()

    with ThreadPoolExecutor(max_workers=WORKERS) as pool:
        futures = {pool.submit(fetch_tile, z, x, y): (z, x, y) for z, x, y in tasks}
        for fut in as_completed(futures):
            z, x, y = futures[fut]
            with lock:
                try:
                    data = fut.result()
                    batch.append((z, x, tms_y(y, z), sqlite3.Binary(data)))
                    count += 1
                except Exception as e:
                    errors += 1
                    print(f"\n  Error z={z} x={x} y={y}: {e}", file=sys.stderr)

                if len(batch) >= BATCH_SIZE:
                    flush()

                print(f"  {count + errors}/{total} ({errors} errors)", end="\r", flush=True)

    if batch:
        flush()

    conn.close()
    size = os.path.getsize(OUTPUT)
    print(f"\nDone: {count} tiles written, {errors} errors")
    print(f"Output: {OUTPUT} ({size / 1024 / 1024:.1f} MB)")

if __name__ == "__main__":
    main()
