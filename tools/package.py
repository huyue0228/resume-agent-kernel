"""独立 Kernel 二进制打包；不访问业务平台、模型或镜像仓库。"""
import argparse
import hashlib
import json
import os
import re
import shutil
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True)
    parser.add_argument("--os", default="linux", choices=["linux", "darwin"])
    parser.add_argument("--arch", default="amd64", choices=["amd64", "arm64"])
    args = parser.parse_args()
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,127}", args.version):
        raise SystemExit("版本必须是安全的单段标识")
    output = ROOT / "dist" / args.version
    output.mkdir(parents=True, exist_ok=False)
    binary = output / f"agent-kernel-{args.os}-{args.arch}"
    subprocess.run(["go", "build", "-trimpath", "-ldflags=-s -w -X main.build=" + args.version,
        "-o", str(binary), "./cmd/agent-kernel"], cwd=ROOT,
        env={**os.environ, "CGO_ENABLED": "0", "GOOS": args.os, "GOARCH": args.arch}, check=True)
    shutil.copyfile(ROOT / "internal/contract/bundle/manifest.json", output / "contract-manifest.json")
    (output / "build.json").write_text(json.dumps(dict(version=args.version, platform=f"{args.os}/{args.arch}",
        commit=subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip(),
        dirty=bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=ROOT, text=True).strip()),
        binary=binary.name), indent=2) + "\n")
    files = sorted(output.iterdir())
    (output / "SHA256SUMS").write_text("".join(hashlib.sha256(p.read_bytes()).hexdigest() + "  " + p.name + "\n" for p in files))
    print(output)

if __name__ == "__main__":
    main()
