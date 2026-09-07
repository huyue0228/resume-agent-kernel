"""独立镜像构建；--push 是唯一会发布到镜像仓库的开关。"""
import argparse
import hashlib
import json
import re
import subprocess
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True)
    parser.add_argument("--image", required=True)
    parser.add_argument("--platform", default="linux/amd64")
    parser.add_argument("--push", action="store_true")
    args = parser.parse_args()
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,127}", args.version):
        raise SystemExit("非法版本")
    if not args.image.endswith(":" + args.version):
        raise SystemExit("镜像 tag 必须与 Kernel build 一致")
    output = ROOT / "release" / args.version
    output.mkdir(parents=True, exist_ok=False)
    with tempfile.TemporaryDirectory(prefix="resume-kernel-image-") as temporary:
        metadata = Path(temporary) / "metadata.json"
        subprocess.run(["docker", "buildx", "build", "--platform", args.platform,
            "--build-arg", "KERNEL_VERSION=" + args.version, "--tag", args.image,
            "--metadata-file", str(metadata), "--push" if args.push else "--load", "."], cwd=ROOT, check=True)
        digest = json.loads(metadata.read_text()).get("containerimage.digest", "")
    if args.push and not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
        raise SystemExit("发布镜像缺少 digest")
    record = output / "image.json"
    record.write_text(json.dumps(dict(version=args.version, image=args.image, digest=digest,
        published=args.push, platform=args.platform,
        commit=subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip(),
        dirty=bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=ROOT, text=True).strip())), indent=2) + "\n")
    (output / "SHA256SUMS").write_text(hashlib.sha256(record.read_bytes()).hexdigest() + "  image.json\n")
    print(output)

if __name__ == "__main__":
    main()
