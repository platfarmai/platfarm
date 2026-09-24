"use strict";

var assert = require("node:assert/strict");
var test = require("node:test");
var crypto = require("node:crypto");
var { pfDecodeEncryptedBody } = require("./pf-crypto-core.js");

function encryptWire(payload, keyBuf) {
  var iv = crypto.randomBytes(12);
  var cipher = crypto.createCipheriv("aes-256-gcm", keyBuf, iv);
  var pt = Buffer.from(JSON.stringify(payload), "utf8");
  var ct = Buffer.concat([cipher.update(pt), cipher.final()]);
  var tag = cipher.getAuthTag();
  var ctWithTag = Buffer.concat([ct, tag]);
  return {
    v: 1,
    alg: "A256GCM",
    kid: "test",
    iv: iv.toString("base64"),
    ct: ctWithTag.toString("base64"),
  };
}

test("pfDecodeEncryptedBody round-trips a known JSON payload", async () => {
  var key = crypto.randomBytes(32);
  var payload = { hello: "world", n: 42, nested: { ok: true } };
  var envelope = encryptWire(payload, key);
  var dataKeyB64 = key.toString("base64");

  var out = await pfDecodeEncryptedBody(envelope, dataKeyB64);
  assert.deepEqual(out, payload);
});

test("pfDecodeEncryptedBody throws when a ciphertext byte is tampered", async () => {
  var key = crypto.randomBytes(32);
  var envelope = encryptWire({ a: 1 }, key);
  var ctBuf = Buffer.from(envelope.ct, "base64");
  ctBuf[0] = ctBuf[0] ^ 0xff;
  envelope.ct = ctBuf.toString("base64");

  await assert.rejects(
    () => pfDecodeEncryptedBody(envelope, key.toString("base64")),
    (err) => err instanceof Error
  );
});
