import { expect, test } from "vitest";
import { goDurationIssue, parseGoDuration } from "./goDuration";

test.each([
  ["0", 0n],
  ["+0", 0n],
  ["-0", 0n],
  ["500ms", 500000000n],
  ["1h2m3.5s", 3723500000000n],
  [".5s", 500000000n],
  ["1.s", 1000000000n],
  ["1µs", 1000n],
  ["1μs", 1000n],
  ["-1.5h", -5400000000000n],
  [".0000000001s", 0n],
  ["9223372036854775807ns", 9223372036854775807n],
  ["-9223372036854775808ns", -9223372036854775808n],
])("parses Go duration %s", (text, want) =>
  expect(parseGoDuration(text as string)).toBe(want),
);

test.each([
  "",
  "00",
  "5",
  "5seconds",
  "1d",
  "1w",
  " 1s",
  "1s ",
  "1s-2s",
  "1..5s",
  ".s",
  "NaNs",
  "Infinityh",
  "9223372036854775808ns",
  "-9223372036854775809ns",
])("rejects invalid Go duration %s", (text) =>
  expect(parseGoDuration(text)).toBeNull(),
);

test("optional and positive/nonnegative requirements are separate from syntax", () => {
  expect(goDurationIssue(undefined)).toBeUndefined();
  expect(goDurationIssue("", { required: true })).toBeDefined();
  expect(goDurationIssue("0")).toBe("Duration must be positive.");
  expect(goDurationIssue("0", { allowZero: true })).toBeUndefined();
  expect(goDurationIssue("-1s", { allowZero: true })).toBe(
    "Duration must be zero or positive.",
  );
  expect(goDurationIssue("1m")).toBeUndefined();
});
