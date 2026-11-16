import itertools
import re
import unittest
import tla_gate
import verification_mutations as mutations


def serial_tlc(code, output, expected):
    errors = re.findall(r'^Error: (.*)$', output, re.M)
    finished = bool(re.search(r'^Finished in .+', output, re.M))
    counts = re.findall(r'([\d,]+) states generated, ([\d,]+) distinct states found, ([\d,]+) states left on queue\.', output)
    if not finished or not counts or int(counts[-1][1].replace(',', '')) <= 0:
        return False
    if expected is None:
        return code == 0 and not errors and int(counts[-1][2].replace(',', '')) == 0 and output.count('Model checking completed. No error has been found.') == 1
    if expected['kind'] == 'invariant':
        return code == 12 and errors == [f"Invariant {expected['name']} is violated.", 'The behavior up to this point is:']
    return code == 13 and errors == ['Temporal properties were violated.', 'The following behavior constitutes a counter-example:']


def serial_passed(case, status, events):
    return status == 0 and any(e.get('Action') == 'pass' and e.get('Test') == case['test'] for e in events)


def serial_killed(case, status, events):
    return (status == 1
            and any(e.get('Action') == 'fail' and e.get('Test') == case['test'] for e in events)
            and any(e.get('Test') == case['test'] and case['diagnostic'] in e.get('Output', '') for e in events)
            and not any(e.get('Action') == 'build-fail' for e in events))


class SerialOracleTest(unittest.TestCase):
    def test_tlc_never_accepts_a_serial_rejection(self):
        bodies = ['Model checking completed. No error has been found.\n',
                  'Error: Invariant Safe is violated.\nError: The behavior up to this point is:\n',
                  'Error: Temporal properties were violated.\nError: The following behavior constitutes a counter-example:\n',
                  'Error: Invariant Other is violated.\n', '']
        counts = ['', '10 states generated, 8 distinct states found, 0 states left on queue.\n',
                  '10 states generated, 8 distinct states found, 1 states left on queue.\n',
                  '10 states generated, 0 distinct states found, 0 states left on queue.\n']
        expectations = [None, {'kind': 'invariant', 'name': 'Safe'}, {'kind': 'property', 'name': 'Live'}]
        for code, body, count, ending, expected in itertools.product(
                (0, 1, 12, 13, 99, -9, -11), bodies, counts, ('', 'Finished in 1s\n', 'Finished in 1'), expectations):
            output = body + count + ending
            if tla_gate.classify(code, output, expected):
                self.assertTrue(serial_tlc(code, output, expected), (code, output, expected))

    def test_mutations_never_accept_a_serial_rejection(self):
        case = {'test': 'TestGuard/lease', 'diagnostic': 'guard missing'}
        choices = [{'Action': 'pass', 'Test': case['test']}, {'Action': 'fail', 'Test': case['test']},
                   {'Action': 'output', 'Test': case['test'], 'Output': case['diagnostic']},
                   {'Action': 'fail', 'Test': 'TestOther'}, {'Action': 'build-fail'},
                   {'Action': 'skip', 'Test': case['test']}, {'Action': 'output', 'Output': 'panic: timed out'}]
        for mask in itertools.product((False, True), repeat=len(choices)):
            prefix = [event for event, keep in zip(choices, mask) if keep]
            for terminal, status in itertools.product(([], [{'Action': 'pass', 'Package': 'p'}],
                                                     [{'Action': 'fail', 'Package': 'p'}]), (0, 1, 2, -9, -11)):
                events = prefix + terminal
                if mutations.passed(case, status, events):
                    self.assertTrue(serial_passed(case, status, events))
                if mutations.killed(case, status, events):
                    self.assertTrue(serial_killed(case, status, events))


if __name__ == '__main__':
    unittest.main()
