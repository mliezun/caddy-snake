"""Minimal pickle-backed key/value store using stdlib sqlite3."""

from __future__ import annotations

import pickle
import sqlite3
import threading
from typing import Any


class KVStore:
    def __init__(self, path: str) -> None:
        self._lock = threading.Lock()
        self._conn = sqlite3.connect(path, check_same_thread=False, timeout=30.0)
        self._conn.execute("PRAGMA journal_mode=WAL")
        self._conn.execute("PRAGMA busy_timeout=30000")
        self._conn.execute("CREATE TABLE IF NOT EXISTS kv (k TEXT PRIMARY KEY, v BLOB NOT NULL)")
        self._conn.commit()

    def __setitem__(self, key: str, value: Any) -> None:
        with self._lock:
            self._conn.execute(
                "INSERT OR REPLACE INTO kv (k, v) VALUES (?, ?)",
                (key, pickle.dumps(value, protocol=pickle.HIGHEST_PROTOCOL)),
            )
            self._conn.commit()

    def __getitem__(self, key: str) -> Any:
        with self._lock:
            row = self._conn.execute("SELECT v FROM kv WHERE k = ?", (key,)).fetchone()
        if row is None:
            raise KeyError(key)
        return pickle.loads(row[0])

    def get(self, key: str, default: Any = None) -> Any:
        try:
            return self[key]
        except KeyError:
            return default

    def __delitem__(self, key: str) -> None:
        with self._lock:
            cur = self._conn.execute("DELETE FROM kv WHERE k = ?", (key,))
            self._conn.commit()
            if cur.rowcount == 0:
                raise KeyError(key)
