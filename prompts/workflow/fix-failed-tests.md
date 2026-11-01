# Fix Failed Tests Prompt

Analyze the supplied phase test report. Reproduce each FAIL in a safe local/test environment. Identify root cause before editing. Fix the implementation without deleting, skipping or weakening the test. Add regression coverage. Run the narrow test and the full relevant suite. If the failure exposes an architectural mismatch, stop and propose an ADR rather than adding an unsafe workaround. Update the phase report and status. Do not start the next phase.
