#!/usr/bin/env python3
"""Run a command on the Brick over SSH (password auth via paramiko).
Usage: brick.py "<remote shell command>"
Reads BRICK_IP, BRICK_USER (default root), BRICK_PW from env.
For file transfer: brick.py --put <local> <remote>   /   --get <remote> <local>
"""
import os, sys, paramiko

HOST = os.environ.get("BRICK_IP")
USER = os.environ.get("BRICK_USER", "root")
PW   = os.environ.get("BRICK_PW", "")
if not HOST:
    sys.exit("set BRICK_IP (and BRICK_PW) in env")

cli = paramiko.SSHClient()
cli.set_missing_host_key_policy(paramiko.AutoAddPolicy())
cli.connect(HOST, username=USER, password=PW, timeout=10, banner_timeout=10, auth_timeout=10)

args = sys.argv[1:]
if args and args[0] == "--put":
    sftp = cli.open_sftp(); sftp.put(args[1], args[2]); sftp.close()
    print(f"put {args[1]} -> {args[2]}")
elif args and args[0] == "--get":
    sftp = cli.open_sftp(); sftp.get(args[1], args[2]); sftp.close()
    print(f"get {args[1]} -> {args[2]}")
else:
    cmd = " ".join(args)
    stdin, stdout, stderr = cli.exec_command(cmd, timeout=60)
    out = stdout.read().decode(errors="replace")
    err = stderr.read().decode(errors="replace")
    sys.stdout.write(out)
    if err.strip(): sys.stderr.write("[stderr]\n" + err)
cli.close()
