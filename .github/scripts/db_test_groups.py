#!/usr/bin/env python3
"""Split the database-backed Go test packages into balanced, disjoint groups.

CI runs the integration packages with `-race -p 1`: serial on purpose, because
their catalog work races on PostgreSQL OIDs and shares one lock table. That
serialism is not negotiable, so the only way to shorten the critical path is to
run several *separate* PostgreSQL servers and give each one a slice of the
packages.

Two properties matter more than the balance itself:

  * The package list is discovered, never hand-written. A new integration
    package joins a group the day it is added. A list that has to be edited by
    hand is a list that silently loses packages.
  * The groups are a partition. Every database package lands in exactly one
    group, so `verify` can prove the split still covers the same inventory the
    single-job workflow used to run.

Weights come from .github/db-test-weights.tsv and only steer the bin packing.
They are a measured hint, not an inventory: an unknown package still gets
placed, it just starts out assumed average.
"""

from __future__ import annotations

import argparse
import os
import re
import subprocess
import sys

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
WEIGHTS_FILE = os.path.join(REPO_ROOT, ".github", "db-test-weights.tsv")

# The environment variable every database-backed test reads to find its server.
DB_ENV_MARKER = "VARYAONE_TEST_DATABASE_URL"


def run(*args: str) -> str:
    return subprocess.run(
        args, cwd=REPO_ROOT, check=True, capture_output=True, text=True
    ).stdout


def module_path() -> str:
    return run("go", "list", "-m").strip()


def all_packages() -> list[str]:
    return sorted(set(run("go", "list", "./...").splitlines()))


def _go_files(root: str):
    """Every .go file under root, skipping what never holds a package."""
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d not in ("testdata", "node_modules")]
        for name in filenames:
            if name.endswith(".go"):
                yield os.path.join(dirpath, name)


def _read(path: str) -> str:
    try:
        with open(path, encoding="utf-8") as fh:
            return fh.read()
    except OSError as err:
        sys.exit(f"cannot read {path}: {err}")


IMPORT_BLOCK = re.compile(r"import\s*\((.*?)\)", re.DOTALL)
IMPORT_LINE = re.compile(r'^\s*(?:[\w.]+\s+)?"([^"]+)"', re.MULTILINE)
IMPORT_SINGLE = re.compile(r'^import\s+(?:[\w.]+\s+)?"([^"]+)"', re.MULTILINE)


def imports_of(source: str) -> set[str]:
    """Every package path a Go file imports."""
    found: set[str] = set()
    for block in IMPORT_BLOCK.findall(source):
        found.update(IMPORT_LINE.findall(block))
    found.update(IMPORT_SINGLE.findall(source))
    return found


def db_packages() -> list[str]:
    """Packages whose tests talk to the shared PostgreSQL service.

    Walks the working tree rather than asking git. `git grep` searches the
    index, so a _test.go file that is not committed yet is invisible to it --
    and an integration test that goes undetected does not fail loudly, it
    quietly joins the parallel unit batch and races the other packages through
    PostgreSQL's shared lock table. Reading the files on disk has no such blind
    spot and needs no tool the runner might not have.

    Detection is two-step, because naming the environment variable is not the
    only way to open a database. A package that gets its connection from a
    shared helper -- `internal/testsupport`, say -- never mentions the variable
    itself, and a purely textual search would file it with the unit tests. So
    the helpers are found first, by the same marker, and then any test file
    importing one of them counts too.
    """
    module, helper_packages, test_files = scan(REPO_ROOT)
    dirs = classify(test_files, helper_packages)

    # An empty result is a hard failure. It would otherwise mean "there are no
    # integration packages", which has never been true and would silently drop
    # the serialisation the whole split depends on.
    if not dirs:
        sys.exit("database-backed test package detection returned no files")

    return [f"{module}/{d}" for d in sorted(dirs)]


def scan(root: str, module: str | None = None):
    """Read a source tree once: which packages are database helpers, and every
    test file with its source.

    A *helper* is a package whose non-test code names the connection variable.
    Only non-test code counts, because only non-test code can be imported: a
    package whose _test.go files open a database is an integration package, not
    something another package can borrow a connection from. Treating those as
    helpers files every package that imports them -- opctl imports backup, and
    opctl touches no database at all.
    """
    if module is None:
        module = module_path()
    helper_dirs: set[str] = set()
    test_files: list[tuple[str, str]] = []
    for top in ("cmd", "internal"):
        directory = os.path.join(root, top)
        if not os.path.isdir(directory):
            continue
        for path in _go_files(directory):
            source = _read(path)
            relative = os.path.relpath(path, root)
            if relative.endswith("_test.go"):
                test_files.append((relative, source))
            elif DB_ENV_MARKER in source:
                helper_dirs.add(os.path.dirname(relative))
    return module, {f"{module}/{d}" for d in helper_dirs}, test_files


def classify(test_files, helper_packages: set[str]) -> set[str]:
    """The directories whose tests need a PostgreSQL server."""
    dirs: set[str] = set()
    for relative, source in test_files:
        directory = os.path.dirname(relative)
        if DB_ENV_MARKER in source:
            dirs.add(directory)
        elif imports_of(source) & helper_packages:
            # The test never names the variable; it gets its connection from a
            # shared helper. Filing it with the unit batch would let it race
            # the integration packages through PostgreSQL's shared lock table.
            dirs.add(directory)
    return dirs


def weights() -> dict[str, float]:
    table: dict[str, float] = {}
    if not os.path.exists(WEIGHTS_FILE):
        return table
    with open(WEIGHTS_FILE, encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            package, _, seconds = line.partition("\t")
            try:
                table[package.strip()] = float(seconds)
            except ValueError:
                continue
    return table


def partition(packages: list[str], group_count: int) -> list[list[str]]:
    """Longest-processing-time greedy bin packing.

    Deterministic: packages are ordered by (-weight, name) so the same input
    always produces the same groups, and ties never depend on dict ordering.
    """
    table = weights()
    # An unmeasured package is assumed average rather than free, so a newly
    # added integration package cannot pile onto the shortest group unnoticed.
    known = [table[p] for p in packages if p in table]
    default = (sum(known) / len(known)) if known else 1.0

    ordered = sorted(packages, key=lambda p: (-table.get(p, default), p))
    groups: list[list[str]] = [[] for _ in range(group_count)]
    totals = [0.0] * group_count
    for package in ordered:
        target = min(range(group_count), key=lambda i: (totals[i], i))
        groups[target].append(package)
        totals[target] += table.get(package, default)
    return [sorted(g) for g in groups]


def require_valid(packages: list[str], module: str) -> None:
    """Never hand `go test` an empty or out-of-module argument.

    An empty argument list makes `go test` fall back to the current directory;
    a stray path would test something the partition never accounted for.
    """
    if not packages:
        sys.exit("refusing to run an empty Go package group")
    for package in packages:
        if package != module and not package.startswith(module + "/"):
            sys.exit(f"refusing invalid Go package argument: '{package}'")


def cmd_group(args: argparse.Namespace) -> None:
    packages = db_packages()
    if not 1 <= args.index <= args.count:
        sys.exit(f"group index {args.index} out of range 1..{args.count}")
    group = partition(packages, args.count)[args.index - 1]
    require_valid(group, module_path())
    print("\n".join(group))


def cmd_verify(args: argparse.Namespace) -> None:
    """Prove the split still runs exactly the packages the old single job ran."""
    module = module_path()
    every = all_packages()
    database = db_packages()
    unit = [p for p in every if p not in set(database)]

    problems: list[str] = []

    if len(every) != len(unit) + len(database):
        problems.append("unit/database package partition is incomplete or overlapping")

    missing = sorted(set(database) - set(every))
    if missing:
        problems.append(f"database packages not in `go list ./...`: {missing}")

    groups = partition(database, args.count)
    flat = [p for g in groups for p in g]

    duplicated = sorted({p for p in flat if flat.count(p) > 1})
    if duplicated:
        problems.append(f"packages present in more than one group: {duplicated}")

    dropped = sorted(set(database) - set(flat))
    if dropped:
        problems.append(f"packages missing from every group: {dropped}")

    extra = sorted(set(flat) - set(database))
    if extra:
        problems.append(f"packages in a group but not database-backed: {extra}")

    for i, group in enumerate(groups, start=1):
        if not group:
            problems.append(f"group {i} of {args.count} is empty")

    table = weights()
    known = [table[p] for p in database if p in table]
    default = (sum(known) / len(known)) if known else 1.0
    print(f"module:            {module}")
    print(f"all packages:      {len(every)}")
    print(f"unit packages:     {len(unit)}")
    print(f"database packages: {len(database)}")
    for i, group in enumerate(groups, start=1):
        total = sum(table.get(p, default) for p in group)
        print(f"\ngroup {i}/{args.count} — {len(group)} packages, ~{total:.0f}s")
        for p in group:
            mark = " " if p in table else "*"
            print(f"  {mark} {table.get(p, default):7.1f}s  {p}")
    unweighted = sorted(set(database) - set(table))
    if unweighted:
        print(f"\n* no measured weight, assumed {default:.1f}s: {len(unweighted)} package(s)")

    if problems:
        print("\nFAILED:", file=sys.stderr)
        for p in problems:
            print(f"  - {p}", file=sys.stderr)
        sys.exit(1)
    print("\nOK: groups are a complete, disjoint partition of the database packages")


def cmd_unit(args: argparse.Namespace) -> None:
    module = module_path()
    database = set(db_packages())
    unit = [p for p in all_packages() if p not in database]
    require_valid(unit, module)
    print("\n".join(unit))


def cmd_all(args: argparse.Namespace) -> None:
    print("\n".join(db_packages()))


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)

    g = sub.add_parser("group", help="print one balanced group of database packages")
    g.add_argument("--index", type=int, required=True)
    g.add_argument("--count", type=int, required=True)
    g.set_defaults(func=cmd_group)

    v = sub.add_parser("verify", help="prove the groups partition the inventory")
    v.add_argument("--count", type=int, required=True)
    v.set_defaults(func=cmd_verify)

    u = sub.add_parser("unit", help="print the non-database packages")
    u.set_defaults(func=cmd_unit)

    a = sub.add_parser("all", help="print every database package")
    a.set_defaults(func=cmd_all)

    args = parser.parse_args()
    args.func(args)


if __name__ == "__main__":
    main()
