#!/usr/bin/env python3
"""Validate title search through the project's private Herdr UI fixture."""
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import tempfile
import time
import unicodedata

ROOT = Path(__file__).resolve().parents[1]
UI = ROOT / ".agents/skills/kanbi-ui-validation/scripts/ui-session.sh"
WORK = Path(tempfile.mkdtemp(prefix="kanbi-search-ui-"))
ENV = dict(os.environ, KANBI_UI_WORKDIR=str(WORK / "fixture"))
WIDTH, HEIGHT = 160, 45
FRAME_INDEX = 0


def ui(*args):
    return subprocess.check_output([str(UI), *map(str, args)], cwd=ROOT, env=ENV, text=True)


def cells(line):
    return sum(0 if unicodedata.combining(c) else
               2 if unicodedata.east_asian_width(c) in ("W", "F") else 1 for c in line)


def frame(label, *expected):
    global FRAME_INDEX
    deadline = time.monotonic() + 6
    while True:
        result = ui("capture")
        if all(text in result for text in expected):
            break
        if time.monotonic() >= deadline:
            raise AssertionError((label, expected, result))
        time.sleep(0.1)
    FRAME_INDEX += 1
    (WORK / f"{FRAME_INDEX:03d}-{label}.txt").write_text(result)
    assert len(result.splitlines()) <= HEIGHT, (label, "height", result)
    assert all(cells(row) <= WIDTH for row in result.splitlines()), (label, "width", result)
    return result


def key(value, label, *expected):
    if value.startswith("Ctrl+"):
        value = value.lower()
    ui("key", value)
    return frame(label, *expected)


def text(value, label, *expected):
    ui("text", value)
    return frame(label, *expected)


def select_board(name):
    result = key("b", "picker", "Select board")
    for _ in range(8):
        if f"> {name}" in result:
            break
        result = key("Down", "picker-move", "Select board")
    else:
        raise AssertionError(("board not found", name, result))
    return key("Enter", "select-board", "KANBI / " + name.upper().split(" (")[0])


def search(query, expected):
    key("/", "open-search", "Type to search titles")
    return text(query, "query", expected)


def resize(width, height):
    global WIDTH, HEIGHT
    WIDTH, HEIGHT = width, height
    ui("resize", width, height)
    return frame("resize")


def main():
    db = None
    try:
        ui("start")
        frame("startup", "Select board")
        db_path = Path(ui("fixture-path").strip())
        assert db_path.parent == WORK / "fixture"
        assert (db_path.parent / ".kanbi-ui-owned").exists()
        db = sqlite3.connect(db_path, timeout=5)
        # Add overflow and Unicode data only through the fixture-scoped CLI.
        cli_env = dict(ENV, KANBI_DB=str(db_path), KANBI_CONFIG=str(db_path.parent / "config.yaml"),
                       KANBI_STATE_DIR=str(db_path.parent / "state"), KANBI_DATA_DIR=str(db_path.parent / "data"))
        for i in range(14):
            subprocess.check_call([str(db_path.parent / "kanbi"), "add", f"UI needle 日本語 [x] {i:02d}",
                                   "--body", "fixture terminal safety", "--board", "agent-kanban"],
                                  env=cli_env, stdout=subprocess.DEVNULL)
        board_id = db.execute("select id from boards where name='agent-kanban'").fetchone()[0]
        ticket_id = db.execute("select id from tickets where board_id=? and title like 'UI needle%' order by id desc limit 1", (board_id,)).fetchone()[0]
        record = json.loads((db_path.parent / "herdr-runtime.json").read_text())
        herdr_env = {k: v for k, v in ENV.items() if not k.startswith("HERDR_")}
        herdr_env["HERDR_SOCKET_PATH"] = record["socket"]

        def herdr(*args):
            output = subprocess.check_output(["herdr", *args], env=herdr_env, text=True)
            return json.loads(output)["result"] if output.strip() else None

        # An owned shell is a read-only inspector target and terminal-input sentinel.
        tab = herdr("tab", "create", "--workspace", record["workspace"], "--cwd", str(WORK),
                    "--label", "search-sentinel", "--no-focus")
        pane = tab["root_pane"]["pane_id"]
        herdr("pane", "run", pane, "printf 'SEARCH_SENTINEL_READY\\n'; cat")
        herdr("pane", "wait-output", pane, "--regex", "SEARCH_SENTINEL_READY", "--source", "recent", "--timeout", "5000")
        db.execute("insert into sessions(ticket_id,harness,tmux_session_name,tmux_window_name,multiplexer,mux_namespace,mux_container_name,mux_container_id,mux_metadata,status,is_active,started_at,created_at,updated_at) values(?,'pi','','','herdr',?,'search-sentinel',?,?,'running',1,datetime('now'),datetime('now'),datetime('now'))",
                   (ticket_id, record["workspace"], pane, json.dumps({"pane_id": pane})))
        db.commit()
        sentinel_before = herdr("pane", "get", pane)
        session_count = db.execute("select count(*) from sessions").fetchone()[0]
        key("Enter", "master", "KANBI / MASTER")
        for size in [(160, 45), (80, 24)]:
            resize(*size)
            select_board("Master (all boards)")
            baseline = frame("master-baseline")
            found = search("homelab", "1/12 matches")
            assert "Bug -" not in found and "attachment" not in found
            key("Ctrl+P", "master-backward-wrap", "12/12 matches")
            key("Enter", "master-forward-wrap", "1/12 matches")
            restored = key("Escape", "master-restored", "?:help")
            assert baseline.splitlines()[1:-2] == restored.splitlines()[1:-2], "Master cancellation lost board anchors"
            key("f", "filters", "Master filters")
            text("personal-finance", "filter-draft", "personal-finance")
            key("Enter", "apply-filter", "filter:")
            search("homelab", "1/3 matches")
            key("Enter", "filtered-next", "2/3 matches")
            key("Escape", "cancel-filtered", "filter:")
            key("f", "reopen-filters", "Master filters")
            key("Ctrl+L", "clear-filter", "?:help")
            select_board("agent-kanban")
            tickets_before = db.execute("select id,column_id,position,title,body,harness from tickets order by id").fetchall()
            baseline = frame("named-baseline")
            search("homelab", "1/3 matches")
            key("Ctrl+P", "named-backward-wrap", "3/3 matches")
            key("Enter", "named-forward-wrap", "1/3 matches")
            restored = key("Escape", "named-restored", "?:help")
            assert baseline.splitlines()[1:-2] == restored.splitlines()[1:-2], "named cancellation lost anchors"
            search("日本語 [x]", "1/14 matches")
            for index in range(2, 15):
                key("Enter", "vertical-next", f"{index}/14 matches")
            overflow = frame("vertical-overflow", "more ▲", "14/14 matches")
            key("Ctrl+G", "keep-ticket", "?:help")
            assert "日本語" in frame("kept-ticket")
            key("i", "open-inspector", "INSPECTOR")
            key("]", "inspector-output", "Output")
            frame("sentinel-output", "SEARCH_SENTINEL_READY")
            key("[", "inspector-context", "Context")
            search("UI needle", "1/14 matches")
            with_inspector = frame("inspector-search")
            assert ("INSPECTOR" in with_inspector) == (WIDTH >= 120)
            board_cells = 106 if WIDTH >= 120 else WIDTH
            assert "fixture terminal safety" not in "\n".join(row[:board_cells] for row in with_inspector.splitlines()), "search enabled inline body preview"
            key("Enter", "inspector-next", "2/14 matches")
            if WIDTH >= 120:
                assert "UI needle 日本語 [x]" in frame("inspector-follows", "INSPECTOR")
            # Resize while searching and while the inspector remains enabled.
            resize(80 if size[0] == 160 else 160, 24 if size[0] == 160 else 45)
            frame("search-resized", "2/14 matches")
            resize(*size)
            key("Escape", "restore-inspector", "INSPECTOR")
            key("i", "close-inspector", "?:help")
            search("missing-ticket", "0 matches")
            assert "No title matches" in frame("no-matches")
            key("Enter", "no-match-enter", "0 matches")
            text("nxHLbfiq?", "shortcut-text", "0 matches")
            assert "Select board" not in frame("shortcuts-isolated")
            key("Escape", "cancel-no-match", "?:help")
            key("/", "empty-query", "Type to search titles")
            key("Enter", "empty-enter", "Type to search titles")
            text("日本語" * 100, "long-query", "0 matches")
            key("Home", "long-query-home", "0 matches")
            text("x", "long-query-insert", "0 matches")
            key("Backspace", "long-query-backspace", "0 matches")
            key("End", "long-query-end", "0 matches")
            key("Escape", "cancel-long-query", "?:help")
            assert db.execute("select id,column_id,position,title,body,harness from tickets order by id").fetchall() == tickets_before
            # Modify the disposable fixture while search is open; poll the actual UI refresh.
            search("UI needle", "1/14 matches")
            selected = db.execute("select id from tickets where board_id=? and title like 'UI needle%' order by position limit 1", (board_id,)).fetchone()[0]
            original_column, original_position = db.execute("select column_id,position from tickets where id=?", (selected,)).fetchone()
            review_column = db.execute("select id from columns where board_id=? and name='Review'", (board_id,)).fetchone()[0]
            db.execute("update tickets set column_id=?, position=99 where id=?", (review_column, selected))
            db.commit()
            frame("external-move", "14/14 matches", "REVIEW")
            db.execute("update tickets set title='Renamed outside query' where id=?", (selected,))
            db.commit()
            frame("external-rename", "1/13 matches")
            db.execute("update tickets set archived_at=datetime('now') where id=?", (selected,))
            db.commit()
            key("Escape", "cancel-after-archive", "?:help")
            db.execute("update tickets set title='UI needle 日本語 [x] restored', archived_at=NULL, column_id=?, position=? where id=?", (original_column, original_position, selected))
            db.commit()
            # Wait for refresh before the next size.
            key("/", "post-mutation-search", "Type to search titles")
            text("UI needle", "post-mutation-query", "/14 matches")
            key("Escape", "post-mutation-cancel", "?:help")
            print(f"Search UI PASS {size[0]}x{size[1]}", flush=True)
        assert db.execute("select count(*) from sessions").fetchone()[0] == session_count, "search created session history"
        sentinel_after = herdr("pane", "get", pane)
        (WORK / "sentinel-before.json").write_text(json.dumps(sentinel_before, indent=2))
        (WORK / "sentinel-after.json").write_text(json.dumps(sentinel_after, indent=2))
        # No sent prompts or search keystrokes should have reached the owned shell.
        output = subprocess.check_output(["herdr", "pane", "read", pane, "--source", "visible"], env=herdr_env, text=True)
        assert "UI needle" not in output and "missing-ticket" not in output
        print(f"Search UI PASS: scope, filters, navigation, wrap, Unicode, overflow, restoration, refresh, inspector, session/input safety. Evidence: {WORK}", flush=True)
    finally:
        if db is not None:
            db.close()
        print(ui("stop"), flush=True)


if __name__ == "__main__":
    main()
