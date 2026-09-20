import { describe, it } from "node:test";
import assert from "node:assert/strict";
import {
  normalizeVersionPayload,
  shortCommit,
} from "./updateCheck_arsydoni.js";

describe("normalizeVersionPayload", () => {
  it("maps the VersionResponse fields verbatim", () => {
    assert.deepEqual(
      normalizeVersionPayload({
        current_version: "v1.12.2",
        has_update: true,
        latest_version: "v1.13.0",
        update_url: "https://example.com/releases",
        latest_commit: "0123456789abcdef",
        current_commit: "9999999",
        changelog: "- a",
      }),
      {
        current_version: "v1.12.2",
        has_update: true,
        latest_version: "v1.13.0",
        update_url: "https://example.com/releases",
        latest_commit: "0123456789abcdef",
        current_commit: "9999999",
        changelog: "- a",
      },
    );
  });

  it("degrades null/absent payloads to empty strings and false", () => {
    assert.deepEqual(normalizeVersionPayload(null), {
      current_version: "",
      has_update: false,
      latest_version: "",
      update_url: "",
      latest_commit: "",
      current_commit: "",
      changelog: "",
    });
    assert.equal(normalizeVersionPayload({ has_update: 1 }).has_update, true);
  });
});

describe("shortCommit", () => {
  it("shortens to 7 chars and tolerates empty input", () => {
    assert.equal(shortCommit("0123456789abcdef"), "0123456");
    assert.equal(shortCommit(""), "");
    assert.equal(shortCommit(undefined), "");
    assert.equal(shortCommit("  abc  "), "abc");
  });
});
