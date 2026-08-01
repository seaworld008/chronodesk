"""Validate the canonical case-evidence manifest without a running API."""

from __future__ import annotations

import csv
import re
import sys
import tempfile
from collections import Counter
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
CASE_DOCUMENT = ROOT / "docs/testing/CHRONODESK_COMPREHENSIVE_TEST_CASES_2026-07-29.md"
MANIFEST = ROOT / "docs/testing/CASE_EVIDENCE_MANIFEST.tsv"
EXPECTED_CASE_COUNT = 237

CASE_PATTERN = re.compile(
    r"^\| ((?:INF|AUTH|RBAC|TKT|CNT|AUT|EVT|AGT|MCP|A2A|UI|SEC|PERF|RES)-\d{3}) \|",
    re.MULTILINE,
)
ALLOWED_COVERAGE = {
    "automated",
    "hybrid",
    "release_manual",
    "fault_injection",
    "ci_gate",
}
ALLOWED_EVIDENCE = {
    "go",
    "pytest",
    "playwright",
    "chrome",
    "manual",
    "fault",
    "ci",
}
DISALLOWED_COVERAGE_MARKERS = {
    "gap",
    "partial",
    "缺口",
    "部分",
    "todo",
    "unknown",
}


def _fail(errors: list[str], message: str) -> None:
    errors.append(message)


def _validate_symbol(
    *,
    kind: str,
    path: Path,
    symbol: str,
    case_id: str,
    errors: list[str],
) -> None:
    text = path.read_text(encoding="utf-8")
    if kind == "go":
        pattern = re.compile(rf"^func {re.escape(symbol)}\s*\(", re.MULTILINE)
        if not pattern.search(text):
            _fail(errors, f"{case_id}: Go 测试符号不存在：{path}::{symbol}")
    elif kind == "pytest":
        pattern = re.compile(
            rf"^\s*(?:async\s+)?def {re.escape(symbol)}\s*\(",
            re.MULTILINE,
        )
        if not pattern.search(text):
            _fail(errors, f"{case_id}: Pytest 测试符号不存在：{path}::{symbol}")
    elif kind == "playwright":
        pattern = re.compile(
            rf"\btest\s*\(\s*(['\"]){re.escape(symbol)}\1",
            re.MULTILINE,
        )
        if not pattern.search(text):
            _fail(
                errors,
                f"{case_id}: Playwright 测试标题不存在：{path}::{symbol}",
            )
    else:
        if symbol != case_id:
            _fail(
                errors,
                f"{case_id}: {kind} 规程 locator 必须使用自身 Case ID，实际为 {symbol}",
            )
        if case_id not in text:
            _fail(errors, f"{case_id}: 规程文件未声明该 Case ID：{path}")


def validate(
    case_document: Path = CASE_DOCUMENT,
    manifest: Path = MANIFEST,
) -> list[str]:
    errors: list[str] = []
    canonical_ids = CASE_PATTERN.findall(case_document.read_text(encoding="utf-8"))
    if (
        len(canonical_ids) != EXPECTED_CASE_COUNT
        or len(set(canonical_ids)) != EXPECTED_CASE_COUNT
    ):
        _fail(
            errors,
            (
                f"正式用例文档必须包含 {EXPECTED_CASE_COUNT} 个唯一 Case ID，"
                f"实际总数={len(canonical_ids)} 唯一数={len(set(canonical_ids))}"
            ),
        )

    with manifest.open(encoding="utf-8", newline="") as handle:
        reader = csv.DictReader(handle, delimiter="\t")
        expected_columns = {
            "case_id",
            "coverage",
            "evidence",
            "execution_record",
        }
        if set(reader.fieldnames or ()) != expected_columns:
            _fail(
                errors,
                (
                    f"manifest 列必须精确为 {sorted(expected_columns)}，"
                    f"实际为 {reader.fieldnames}"
                ),
            )
        rows = list(reader)

    manifest_ids = [row.get("case_id", "").strip() for row in rows]
    duplicates = sorted(
        case_id for case_id, count in Counter(manifest_ids).items() if count > 1
    )
    missing = sorted(set(canonical_ids) - set(manifest_ids))
    unknown = sorted(set(manifest_ids) - set(canonical_ids))
    if duplicates:
        _fail(errors, f"manifest 存在重复 Case ID：{duplicates}")
    if missing:
        _fail(errors, f"manifest 缺少 Case ID：{missing}")
    if unknown:
        _fail(errors, f"manifest 存在未知 Case ID：{unknown}")
    if len(rows) != EXPECTED_CASE_COUNT:
        _fail(
            errors,
            f"manifest 必须恰有 {EXPECTED_CASE_COUNT} 行，实际为 {len(rows)}",
        )

    for row in rows:
        case_id = row.get("case_id", "").strip()
        coverage = row.get("coverage", "").strip()
        lowered_coverage = coverage.lower()
        if coverage not in ALLOWED_COVERAGE:
            _fail(errors, f"{case_id}: 非法 coverage：{coverage!r}")
        if any(marker in lowered_coverage for marker in DISALLOWED_COVERAGE_MARKERS):
            _fail(errors, f"{case_id}: coverage 不得标记为缺口/部分：{coverage!r}")
        if row.get("execution_record", "").strip() != "not_recorded":
            _fail(
                errors,
                (
                    f"{case_id}: manifest 不是执行结果台账，"
                    "execution_record 必须为 not_recorded"
                ),
            )

        evidence_items = [
            item.strip() for item in row.get("evidence", "").split(";") if item.strip()
        ]
        if not evidence_items:
            _fail(errors, f"{case_id}: 缺少证据 locator")
            continue
        for evidence in evidence_items:
            if "@" not in evidence:
                _fail(errors, f"{case_id}: 证据缺少 type@locator：{evidence!r}")
                continue
            kind, locator = evidence.split("@", 1)
            if kind not in ALLOWED_EVIDENCE:
                _fail(errors, f"{case_id}: 未知证据类型 {kind!r}")
                continue
            if kind == "ci":
                relative_path = locator
                symbol = ""
            elif "::" in locator:
                relative_path, symbol = locator.split("::", 1)
            else:
                _fail(errors, f"{case_id}: locator 缺少 ::symbol：{locator!r}")
                continue

            path = ROOT / relative_path
            if not path.is_file():
                _fail(errors, f"{case_id}: 证据文件不存在：{relative_path}")
                continue
            if kind != "ci":
                _validate_symbol(
                    kind=kind,
                    path=path,
                    symbol=symbol,
                    case_id=case_id,
                    errors=errors,
                )

    return errors


def _self_test() -> list[str]:
    failures: list[str] = []
    source_lines = MANIFEST.read_text(encoding="utf-8").splitlines()
    mutations = {
        "缺少 Case ID": source_lines[:-1],
        "未知 Case ID": [
            source_lines[0],
            source_lines[1].replace("INF-001", "INF-999", 1),
            *source_lines[2:],
        ],
        "部分 coverage": [
            source_lines[0],
            source_lines[1].replace("\tfault_injection\t", "\tpartial\t", 1),
            *source_lines[2:],
        ],
    }
    with tempfile.TemporaryDirectory(prefix="chronodesk-evidence-") as directory:
        temporary_directory = Path(directory)
        for label, lines in mutations.items():
            mutated = temporary_directory / f"{label}.tsv"
            mutated.write_text("\n".join(lines) + "\n", encoding="utf-8")
            if not validate(manifest=mutated):
                failures.append(f"{label} 变体未被拒绝")
    return failures


def main() -> int:
    if sys.argv[1:] == ["--self-test"]:
        failures = _self_test()
        if failures:
            for failure in failures:
                print(f"- {failure}", file=sys.stderr)
            return 1
        print("Manifest 校验器自检通过：缺 ID、未知 ID、部分 coverage 均被拒绝。")
        return 0
    if sys.argv[1:]:
        print("用法：validate_case_evidence_manifest.py [--self-test]", file=sys.stderr)
        return 2

    errors = validate()
    if errors:
        print("Case Evidence Manifest 校验失败：", file=sys.stderr)
        for error in errors:
            print(f"- {error}", file=sys.stderr)
        return 1
    print(
        "Case Evidence Manifest 校验通过："
        f"{EXPECTED_CASE_COUNT}/{EXPECTED_CASE_COUNT} Case ID 均有可定位证据。"
    )
    print("说明：execution_record=not_recorded，不代表相关测试已经执行或通过。")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
