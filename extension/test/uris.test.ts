import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { parseVersionQuery, referenceText, relativePathFromUriPath, versionQuery, versionUriPath } from "../src/uris";

describe("version URIs", () => {
  it("keeps the real file path with a leading slash", () => {
    assert.equal(versionUriPath("internal/store/store.go"), "/internal/store/store.go");
    assert.equal(versionUriPath("/already/rooted.go"), "/already/rooted.go");
    assert.equal(relativePathFromUriPath("/internal/store/store.go"), "internal/store/store.go");
  });

  it("round-trips hash, event, label and root through the query", () => {
    const ref = { root: "/home/me/my proj", path: "a&b.go", hash: "2c26b46", event: "01JB4X9M", label: "before 14:32" };
    const parsed = parseVersionQuery(versionQuery(ref));
    assert.deepEqual(parsed, { root: "/home/me/my proj", hash: "2c26b46", event: "01JB4X9M", label: "before 14:32" });
  });

  it("encodes a missing version as an empty hash", () => {
    const query = versionQuery({ root: "/r", path: "x", hash: null, event: "01JB", label: "new" });
    assert.match(query, /hash=&/);
    assert.equal(parseVersionQuery(query).hash, null);
  });

  it("tolerates a malformed query", () => {
    assert.deepEqual(parseVersionQuery(""), { root: "", hash: null, event: "", label: "" });
  });

  it("formats the prompt reference", () => {
    assert.equal(referenceText("01JB4X9M", "src/a.go"), "pawprints:01JB4X9M src/a.go");
  });
});
