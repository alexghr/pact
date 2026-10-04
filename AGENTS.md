# Project guidance

Use the project's devenv environment for development and checks: `./shell p-ci`.
Build the launcher with `./shell p-build`.

## Testing

Keep tests minimal and focused on meaningful logic and behavior. Do not add tests
for simple argument forwarding, command construction, or config code that only
reads a file and parses JSON. These tests mostly repeat the implementation or
check standard-library behavior and provide little value here.

Test behavior with a meaningful failure mode, such as whether the configured Git
identity actually produces a signed commit that can be verified. Do not add tests
solely for coverage or to give every module a test file.
