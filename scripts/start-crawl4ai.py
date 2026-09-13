"""Initialize local reader credentials without printing them, then start Docker."""
from pathlib import Path
import re
import secrets
import subprocess


def main():
    root = Path(__file__).resolve().parents[1]
    env = root / ".env"
    text = env.read_bytes().decode("utf-8") if env.exists() else ""
    newline = "\r\n" if "\r\n" in text else "\n"
    for name, default in (
        ("CRAWL4AI_URL", "http://127.0.0.1:11235"),
        ("CRAWL4AI_API_TOKEN", secrets.token_hex(32)),
    ):
        pattern = re.compile(r"^" + name + r"=([^\r\n]*)", re.MULTILINE)
        match = pattern.search(text)
        if match and match.group(1).strip().strip("\"'"):
            continue
        line = name + "=" + default
        if match:
            text = pattern.sub(lambda _: line, text)
        else:
            if text and not text.endswith("\n"):
                text += newline
            text += line + newline
    env.write_bytes(text.encode("utf-8"))
    subprocess.run(
        ["docker", "compose", "--profile", "research", "up", "-d", "crawl4ai"],
        cwd=root, check=True,
    )
    print("网页读取服务已启动；已有 buildsvc 进程需重启以读取新配置。")


if __name__ == "__main__":
    main()
