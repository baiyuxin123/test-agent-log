#!/usr/bin/env python3
import argparse
import json
import os
import sys
import time


DEFAULT_PROJECT = "k8s-log-cc338bd1e67fa4d8e9be4ad1e9435670a"
DEFAULT_LOGSTORE = "dsers-app"


try:
    from dotenv import load_dotenv
except ModuleNotFoundError:
    load_dotenv = None
else:
    load_dotenv()


def getenv_required(name: str) -> str:
    value = os.environ.get(name)
    if not value:
        raise SystemExit(f"Missing required environment variable: {name}")
    return value


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Query Alibaba Cloud SLS logs.")
    parser.add_argument(
        "-q",
        "--query",
        default="* | select * limit 20",
        help="SLS query or query-and-analysis statement.",
    )
    parser.add_argument(
        "--minutes",
        type=int,
        default=60,
        help="Lookback window in minutes. Default: 60.",
    )
    parser.add_argument("--from-time", type=int, help="Start UNIX timestamp in seconds.")
    parser.add_argument("--to-time", type=int, help="End UNIX timestamp in seconds.")
    parser.add_argument("--line", type=int, default=100, help="Max search rows. Default: 100.")
    parser.add_argument("--offset", type=int, default=0, help="Search offset. Default: 0.")
    parser.add_argument(
        "--reverse",
        action="store_true",
        help="Return search logs newest first. SQL statements should use ORDER BY instead.",
    )
    parser.add_argument(
        "--project",
        default=os.environ.get("ALIYUN_SLS_PROJECT", DEFAULT_PROJECT),
        help="SLS Project name.",
    )
    parser.add_argument(
        "--logstore",
        default=os.environ.get("ALIYUN_SLS_LOGSTORE", DEFAULT_LOGSTORE),
        help="SLS Logstore name.",
    )
    parser.add_argument(
        "--endpoint",
        default=os.environ.get("ALIYUN_SLS_ENDPOINT"),
        help="SLS endpoint, for example cn-hangzhou.log.aliyuncs.com.",
    )
    parser.add_argument(
        "--pretty",
        action="store_true",
        help="Pretty-print JSON output.",
    )
    return parser


def main() -> int:
    args = build_parser().parse_args()

    try:
        from aliyun.log import GetLogsRequest, LogClient
    except ModuleNotFoundError as exc:
        if exc.name == "aliyun":
            raise SystemExit(
                "Missing dependency: aliyun-log-python-sdk. "
                "Install it with `pip install -r requirements.txt`."
            ) from exc
        raise

    endpoint = args.endpoint or getenv_required("ALIYUN_SLS_ENDPOINT")
    access_key_id = getenv_required("ALIBABA_CLOUD_ACCESS_KEY_ID")
    access_key_secret = getenv_required("ALIBABA_CLOUD_ACCESS_KEY_SECRET")
    security_token = os.environ.get("ALIBABA_CLOUD_SECURITY_TOKEN")

    to_time = args.to_time or int(time.time())
    from_time = args.from_time or (to_time - args.minutes * 60)
    if from_time >= to_time:
        raise SystemExit("--from-time must be earlier than --to-time")

    client = LogClient(endpoint, access_key_id, access_key_secret, securityToken=security_token)
    request = GetLogsRequest(
        args.project,
        args.logstore,
        from_time,
        to_time,
        topic="",
        query=args.query,
        line=args.line,
        offset=args.offset,
        reverse=args.reverse,
    )

    response = client.get_logs(request)
    rows = [dict(log.contents) for log in response.get_logs()]
    result = {
        "completed": response.is_completed(),
        "count": len(rows),
        "from": from_time,
        "to": to_time,
        "project": args.project,
        "logstore": args.logstore,
        "rows": rows,
    }

    indent = 2 if args.pretty else None
    print(json.dumps(result, ensure_ascii=False, indent=indent))
    if not response.is_completed():
        print(
            "Warning: SLS reports this query is not complete. Retry with a narrower time range.",
            file=sys.stderr,
        )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
