"use strict";

/**
 * Pure AES-256-GCM decode for Platfarm encrypted JSON responses.
 * Wire: { v:1, alg:"A256GCM", kid, iv:<b64>, ct:<b64> } where ct = ciphertext||tag.
 * No DOM; works under Node (crypto.subtle) and browsers.
 */

function b64ToBytes(b64) {
  var bin = atob(b64);
  var out = new Uint8Array(bin.length);
  for (var i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

async function pfDecodeEncryptedBody(envelope, dataKeyB64) {
  if (!envelope || envelope.v !== 1 || envelope.alg !== "A256GCM") {
    throw new Error("unsupported encrypted response envelope");
  }
  if (typeof dataKeyB64 !== "string" || !dataKeyB64) {
    throw new Error("missing data key");
  }
  var keyBytes = b64ToBytes(dataKeyB64);
  var iv = b64ToBytes(envelope.iv);
  var ct = b64ToBytes(envelope.ct);
  var subtle = globalThis.crypto && globalThis.crypto.subtle;
  if (!subtle) throw new Error("WebCrypto subtle unavailable");

  var key = await subtle.importKey("raw", keyBytes, { name: "AES-GCM" }, false, [
    "decrypt",
  ]);
  var plainBuf = await subtle.decrypt({ name: "AES-GCM", iv: iv }, key, ct);
  return JSON.parse(new TextDecoder().decode(plainBuf));
}

var api = { b64ToBytes: b64ToBytes, pfDecodeEncryptedBody: pfDecodeEncryptedBody };

if (typeof module === "object" && module.exports) {
  module.exports = api;
} else if (typeof globalThis !== "undefined") {
  globalThis.pfCryptoCore = api;
}
