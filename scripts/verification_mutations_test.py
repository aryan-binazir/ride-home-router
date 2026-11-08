import unittest
import verification_mutations as mutations


class MutationVerdictTest(unittest.TestCase):
    case = {"test": "TestGuard/lease", "diagnostic": "expired claim wrote"}

    def test_only_named_assertion_kills_mutant(self):
        events = [{"Action": "output", "Test": "TestGuard/lease", "Output": "expired claim wrote"},
                  {"Action": "fail", "Test": "TestGuard/lease"}]
        self.assertTrue(mutations.killed(self.case, 1, events))
        self.assertFalse(mutations.killed(self.case, 0, events))
        self.assertFalse(mutations.killed(self.case, 1, events + [{"Action": "build-fail"}]))
        self.assertFalse(mutations.killed(self.case, 1, [events[0], {"Action": "fail", "Test": "TestOther"}]))
        self.assertFalse(mutations.killed(self.case, 1, [{"Action": "fail", "Test": "TestGuard/lease"}]))

    def test_skip_or_package_pass_is_not_a_control_pass(self):
        self.assertFalse(mutations.passed(self.case, 0, [{"Action": "skip", "Test": "TestGuard/lease"}]))
        self.assertFalse(mutations.passed(self.case, 0, [{"Action": "pass", "Package": "example"}]))
        self.assertTrue(mutations.passed(self.case, 0, [{"Action": "pass", "Test": "TestGuard/lease"}]))


if __name__ == "__main__":
    unittest.main()
