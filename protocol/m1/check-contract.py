#!/usr/bin/env python3
"""Validate baseline fixtures; this does not implement or test a bridge daemon.

Dependency: jsonschema >= 4 with Draft202012Validator/FormatChecker.
Run: rtk proxy python3 protocol/m1/check-contract.py
"""
from __future__ import annotations

import hashlib
import json
import math
import re
from datetime import datetime
from decimal import Decimal
from pathlib import Path
from urllib.parse import urlsplit

from jsonschema import Draft202012Validator, FormatChecker

ROOT = Path(__file__).resolve().parent
MAX_INTEGER = 9007199254740991
FORMATS = FormatChecker()
# RFC 3986 ASCII URI grammar; resource locators additionally forbid userinfo.
UNRESERVED = r"[A-Za-z0-9._~-]"
PERCENT = r"%[0-9A-Fa-f]{2}"
SUBDELIM = r"[!$&'()*+,;=]"
PCHAR = rf"(?:{UNRESERVED}|{PERCENT}|{SUBDELIM}|[:@])"
REGNAME = rf"(?:{UNRESERVED}|{PERCENT}|{SUBDELIM})*"
IP_LITERAL = rf"\[(?:[0-9A-Fa-f:.]+|[vV][0-9A-Fa-f]+\.(?:{UNRESERVED}|{SUBDELIM}|:)+)\]"
AUTHORITY = rf"(?:{IP_LITERAL}|{REGNAME})(?::[0-9]*)?"
ABEMPTY = rf"(?:/{PCHAR}*)*"
ROOTLESS = rf"{PCHAR}+(?:/{PCHAR}*)*"
ABSOLUTE = rf"/(?:{ROOTLESS})?"
HIERARCHY = rf"(?://{AUTHORITY}{ABEMPTY}|{ABSOLUTE}|{ROOTLESS}|)"
QUERY = rf"(?:{PCHAR}|[/?])*"
URI = re.compile(rf"[A-Za-z][A-Za-z0-9+.-]*:{HIERARCHY}(?:\?{QUERY})?(?:#{QUERY})?")


@FORMATS.checks("date-time", raises=(ValueError, TypeError))
def millisecond_utc(value):
    if not isinstance(value, str):
        return True
    datetime.strptime(value, "%Y-%m-%dT%H:%M:%S.%fZ")
    return True


@FORMATS.checks("uri", raises=(ValueError, TypeError))
def absolute_uri(value):
    if not isinstance(value, str):
        return True
    if not URI.fullmatch(value):
        return False
    parse_value = re.sub(r"^(.*?://\[)V(?=[0-9A-Fa-f]+\.)", r"\1v", value)
    parts = urlsplit(parse_value)
    return bool(parts.scheme) and parts.username is None and parts.password is None


def pairs(items):
    result = {}
    for key, value in items:
        if key in result:
            raise ValueError("duplicate key")
        result[key] = value
    return result


def validate_value(value):
    if isinstance(value, str):
        value.encode("utf-8", errors="strict")
    elif isinstance(value, bool) or value is None:
        return
    elif isinstance(value, Decimal):
        if not value.is_finite() or abs(value) > MAX_INTEGER or value != value.to_integral_value():
            raise ValueError("not an exact safe integer")
    elif isinstance(value, (int, float)):
        if not math.isfinite(value) or int(value) != value or abs(value) > MAX_INTEGER:
            raise ValueError("not a safe integer")
    elif isinstance(value, list):
        for item in value:
            validate_value(item)
    elif isinstance(value, dict):
        for key, item in value.items():
            validate_value(key)
            validate_value(item)
    else:
        raise ValueError("not JSON")


def parse(raw):
    value = json.loads(raw, object_pairs_hook=pairs, parse_float=Decimal)
    validate_value(value)
    return value


def canonical(value):
    """RFC 8785 serialization for this profile's safe-integer-only subset."""
    validate_value(value)
    if isinstance(value, dict):
        keys = sorted(value, key=lambda key: key.encode("utf-16be"))
        return "{" + ",".join(canonical(key) + ":" + canonical(value[key]) for key in keys) + "}"
    if isinstance(value, list):
        return "[" + ",".join(canonical(item) for item in value) + "]"
    if isinstance(value, (int, float, Decimal)) and not isinstance(value, bool):
        return str(int(value))
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"), allow_nan=False)


def main():
    schema = json.loads((ROOT / "schema.json").read_text())
    vectors = json.loads((ROOT / "golden-vectors.json").read_text())
    Draft202012Validator.check_schema(schema)
    failures = []
    for case in vectors["schema_cases"]:
        scoped = {"$schema": schema["$schema"], "$defs": schema["$defs"],
                  "$ref": "#/$defs/" + case["definition"]}
        valid = Draft202012Validator(scoped, format_checker=FORMATS).is_valid(case["value"])
        if valid != case["valid"]:
            failures.append(case["id"] + ": unexpected schema outcome")
    hashes = {}
    for case in vectors["hash_cases"]:
        request = case["request"]
        params = {key: value for key, value in request["params"].items() if key != "idempotency_key"}
        payload = {"protocol_version": "bridge-m1-1", "method": request["method"], "params": params}
        actual = canonical(payload)
        digest = hashlib.sha256(actual.encode()).hexdigest()
        hashes[case["id"]] = digest
        if actual != case["canonical"] or digest != case["sha256"]:
            failures.append(case["id"] + ": canonical bytes/hash mismatch")
    if hashes["G05_base"] != hashes["G05_key_order_and_rpc_id"]:
        failures.append("object order/RPC id must not change hash")
    for left, right in [("G05_base", "G05_revision_changed"),
                        ("G05_base", "G05_extension_null"),
                        ("G05_array_order_A", "G05_array_order_B")]:
        if hashes[left] == hashes[right]:
            failures.append(left + "/" + right + ": must differ")
    for case in vectors["parser_cases"]:
        try:
            actual = canonical(parse(case["json"]))
            valid = True
        except (ValueError, UnicodeError):
            valid, actual = False, None
        if valid != case["valid"] or (valid and actual != case["canonical"]):
            failures.append(case["id"] + ": parser outcome mismatch")
    methods = {item["properties"]["method"]["const"] for item in schema["$defs"]["Request"]["oneOf"]}
    request_methods = {case["value"]["method"] for case in vectors["schema_cases"]
                       if case["definition"] == "Request" and case["valid"]}
    if methods != request_methods or methods != set(vectors["method_names"]):
        failures.append("method list/request fixture coverage mismatch")
    if failures:
        raise SystemExit("\n".join(failures))
    print(f"PASS: schema + {len(vectors['schema_cases'])} structural cases + "
          f"{len(vectors['hash_cases'])} digest cases + {len(vectors['parser_cases'])} parser cases; "
          f"{len(methods)} RPC request shapes covered.")
    print("NOT RUN: bridge state/auth/concurrency/persistence, H01/V01–V25, independent client conformance.")


if __name__ == "__main__":
    main()
