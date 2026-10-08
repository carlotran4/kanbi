#!/usr/bin/env python3
"""Drive the project's isolated real tmux fixture and retain checked frames.

This measures functional behavior, not input-to-paint latency. Every navigation
key is sent individually, then its rendered terminal frame is inspected.
"""
import os
from pathlib import Path
import re
import sqlite3
import subprocess
import time
import unicodedata

ROOT = Path(__file__).resolve().parents[1]
UI = ROOT / ".pi/skills/kanbi-ui-validation/scripts/ui-session.sh"
width, height = 160, 45


def ui(*args):
    return subprocess.check_output([str(UI), *args], text=True, cwd=ROOT)


def cells(line):
    return sum(0 if unicodedata.combining(c) else
               2 if unicodedata.east_asian_width(c) in ("W", "F") else 1
               for c in line)


def capture(label, *expected, selected=False):
    time.sleep(0.12)
    frame = ui("capture")
    print(f"UI FRAME {label} {width}x{height}\n{frame}", flush=True)
    lines = frame.splitlines()
    assert len(lines) <= height, (label, len(lines), height)
    assert all(cells(line) <= width for line in lines), (label, "width overflow")
    assert "Kanbi ·" in frame and "?:help" in frame, (label, "missing header/footer")
    for text in expected:
        assert text in frame, (label, "missing", text)
    if selected:
        assert re.search(r"│\s*> T-\d+", frame), (label, "selected card invisible")
    return frame


def key(value, label=None, *expected, selected=False):
    ui("key", value)
    return capture(label or value, *expected, selected=selected)


def resize(w, h):
    global width, height
    width, height = w, h
    ui("resize", str(w), str(h))
    return capture("resize", selected=True)


def main():
    print(ui("start"), flush=True)
    try:
        capture("startup", "Select board")
        key("Enter", "Master", "Master", selected=True)
        for size in [(160, 40), (80, 24)]:
            resize(*size)
            for value in ["j"] * 9 + ["k"] * 4 + ["l"] * 3 + ["h"] * 3:
                key(value, selected=True)
            key("?", "help", "legend")
            key("Escape", selected=True)
            key("f", "Master filters", "filter")
            key("Escape", selected=True)
            key("b", "board picker", "Select board")
            key("j", "named board selection", "Select board")
            frame = key("Enter", "named board", "agent-kanban", selected=True)
            display_id = re.search(r"│\s*> (T-\d+)", frame).group(1)
            db = sqlite3.connect(ui("fixture-path").strip(), timeout=5)
            ticket_id, title = db.execute(
                "select t.id,t.title from tickets t join boards b on b.id=t.board_id "
                "where b.name='agent-kanban' and t.display_id=?", (display_id,)).fetchone()
            # Different note bodies expose stale-cache mistakes.
            db.executemany("insert into ticket_notes(ticket_id,body,created_at,updated_at) "
                           "values(?,?,datetime('now'),datetime('now'))",
                           [(ticket_id, f"UI note {i:02d} **markdown**") for i in range(50)])
            db.commit()
            key("e", "inspector", "Notes", "Ctrl+S")
            ui("text", "PERF_DRAFT_")
            capture("unsaved title", "PERF_DRAFT_", "unsaved")
            time.sleep(2.2)
            capture("draft survives runtime tick", "PERF_DRAFT_", "unsaved")
            key("Escape", selected=True)
            assert db.execute("select title from tickets where id=?", (ticket_id,)).fetchone()[0] == title
            key("e", "reopen inspector", "Notes")
            key("BTab", "notes tab", "UI note 49")
            key("k", "previous note", "UI note 48")
            key("e", "edit note", "Ctrl+S")
            ui("text", "PERF_NOTE_EDIT_")
            capture("typed note", "PERF_NOTE_EDIT_")
            key("C-s", "save note", "PERF_NOTE_EDIT_")
            assert db.execute("select count(*) from ticket_notes where ticket_id=? and body like '%PERF_NOTE_EDIT_%'", (ticket_id,)).fetchone()[0] == 1
            key("a", "new note", "Ctrl+S")
            ui("text", "PERF_NEW_NOTE")
            key("C-s", "save new note", "PERF_NEW_NOTE")
            key("d", "delete new note", "UI note")
            assert db.execute("select count(*) from ticket_notes where ticket_id=? and body='PERF_NEW_NOTE'", (ticket_id,)).fetchone()[0] == 0
            key("Escape", "leave notes", "Notes")
            key("Escape", selected=True)
            # Use a long body containing Unicode and an image reference; render
            # placeholders on the real xterm fixture, then edit and save it.
            body = "Unicode café 界\n![](/tmp/performance-missing.png)\n" + "markdown **body** " * 3800
            db.execute("update tickets set body=? where id=?", (body, ticket_id))
            db.commit()
            time.sleep(2.2)
            key("e", "long-body inspector", "image:")
            key("Tab", "body textarea", "Ctrl+V")
            ui("text", "PERF_BODY_EDIT_")
            capture("long-body typing", "PERF_BODY_EDIT_")
            key("C-s", "save body", selected=True)
            assert "PERF_BODY_EDIT_" in db.execute("select body from tickets where id=?", (ticket_id,)).fetchone()[0]
            # An actual SQLite writer runs across a polling tick; UI navigation
            # and an unsaved editor remain usable while observations wait.
            db.execute("begin immediate")
            db.execute("update tickets set updated_at=updated_at where id=?", (ticket_id,))
            time.sleep(2.2)
            key("j", "writer contention navigation", selected=True)
            key("k", "writer contention navigation", selected=True)
            key("e", "writer contention inspector", "Notes")
            ui("text", "PERF_LOCK_DRAFT_")
            capture("writer contention draft", "PERF_LOCK_DRAFT_")
            db.rollback()
            time.sleep(2.2)
            capture("recovery preserves draft", "PERF_LOCK_DRAFT_")
            key("Escape", selected=True)
            db.close()
            key("b", "return picker", "Select board")
            key("k", "Master selection", "Master")
            key("Enter", "return Master", "Master", selected=True)
        print("UI PASS: Master/named boards, navigation/overflow, resize, filters/help, "
              "unsaved drafts, 50 notes edit/add/delete, 64KiB body edit/save, "
              "Unicode/image placeholder, SQLite writer contention", flush=True)
    finally:
        print(ui("stop"), flush=True)


if __name__ == "__main__":
    main()
