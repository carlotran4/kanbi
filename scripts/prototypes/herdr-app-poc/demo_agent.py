#!/usr/bin/env python3
"""Offline interactive test harness. Not an AI model and no fabricated resume ref."""
import base64
import sys
import time

def status(state,msg=''):
    fields='state='+state+':app=kanbi-demo:msg='+base64.b64encode(msg.encode()).decode()
    if state=='blocked': fields+=':kind=permission'
    print('\x1b]7501;'+fields+'\x1b\\',end='',flush=True)
print('\x1b]0;Kanbi offline demo\x07',end='')
print('KANBI OFFLINE DEMO — no model or network requests')
print('This is a real core terminal managed by a real Kanbi session.')
print('Try: hello | work | permission | approve | quit')
if len(sys.argv)>1:
    print('\nTicket prompt received:\n'+sys.argv[-1]+'\n')
status('idle','Ready for input')
while True:
    try:line=input('demo> ')
    except (EOFError,KeyboardInterrupt):break
    if line.strip() in ('quit','exit','/exit'):break
    if line.strip()=='work':
        status('working','Running an offline demonstration');print('Working…',flush=True);time.sleep(1);print('Finished the offline task.');status('done','Offline task complete')
    elif line.strip()=='permission':
        print('Permission requested — type approve to continue.');status('blocked','permission: awaiting explicit approval')
    else:
        print('Received literal input: '+line);status('idle','Ready for input')
print('Demo closed. Kanbi keeps the session history.')
