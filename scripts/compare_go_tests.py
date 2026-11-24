"""Compare complete Go JSON test run/pass inventories, including subtests."""
import collections
import json
from pathlib import Path
import sys


def inventory(path):
    actions = collections.defaultdict(collections.Counter)
    for line in Path(path).read_text().splitlines():
        try:
            event = json.loads(line)
        except ValueError:
            continue
        if event.get('Action') == 'fail':
            raise ValueError(f'{path}: failed test or package: {event}')
        if event.get('Test') and event.get('Action') in ('run', 'pass', 'skip'):
            actions[event['Action']][(event['Package'], event['Test'])] += 1
    if not actions['run'] or actions['run'] != actions['pass'] + actions['skip']:
        raise ValueError(f'{path}: incomplete or failing test inventory')
    allowed = {('github.com/aryan-binazir/ride-home-router/internal/routing/planeval', 'TestPlannerEvaluation')}
    if set(actions['skip']) - allowed:
        raise ValueError(f'{path}: unexpected skips: {actions["skip"]}')
    return actions


def main():
    if len(sys.argv) < 3:
        raise SystemExit('usage: compare_go_tests.py BASELINE CANDIDATE [CANDIDATE ...]')
    baseline = inventory(sys.argv[1])
    for path in sys.argv[2:]:
        candidate = inventory(path)
        for action in ('run', 'pass', 'skip'):
            if baseline[action] != candidate[action]:
                raise ValueError(f'{path}: {action}: missing={baseline[action]-candidate[action]}, extra={candidate[action]-baseline[action]}')
        print(f'{path}: identical: {sum(candidate["run"].values())} run, {sum(candidate["pass"].values())} pass, {sum(candidate["skip"].values())} skip (including subtests)')


if __name__ == '__main__':
    main()
