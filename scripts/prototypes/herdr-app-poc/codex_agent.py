#!/usr/bin/env python3
"""Explicit real-Codex opt-in, with independent process ownership and sandbox."""
import json
import os
from pathlib import Path
import sys
settings=json.loads((Path(__file__).resolve().parent.parent/'codex.json').read_text())
argv=[settings['binary'],'--no-daemon','--sandbox','workspace-write','--ask-for-approval','on-request']
argv.extend(sys.argv[1:])
os.execv(argv[0],argv)
