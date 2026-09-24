"use strict";

/**
 * Browser helper for Platfarm response-body decryption.
 * Zero dependencies. Uses pf-crypto-core for the pure decode path.
 */
(function (root) {
  var core;
  if (typeof require === "function") {
    try {
      core = require("./pf-crypto-core.js");
    } catch (_) {
      core = null;
    }
  }
  if (!core && root && root.pfCryptoCore) core = root.pfCryptoCore;
  if (!core) throw new Error("pf-crypto-core unavailable");

  async function pfDecryptResponse(response, dataKeyB64) {
    if (!response || typeof response.headers.get !== "function") {
      throw new Error("expected a fetch Response");
    }
    if (!response.headers.get("X-PF-Encrypted")) {
      return response.json();
    }
    var envelope = await response.json();
    return core.pfDecodeEncryptedBody(envelope, dataKeyB64);
  }

  function pfInstallFetch(getKey) {
    if (typeof getKey !== "function") throw new Error("getKey must be a function");
    if (!root || typeof root.fetch !== "function") {
      throw new Error("window.fetch unavailable");
    }
    var original = root.fetch.bind(root);
    root.fetch = function () {
      var args = arguments;
      return original.apply(root, args).then(function (response) {
        if (!response.headers.get("X-PF-Encrypted")) return response;
        var key = getKey();
        var origJson = response.json.bind(response);
        response.json = function () {
          return origJson().then(function (envelope) {
            return core.pfDecodeEncryptedBody(envelope, key);
          });
        };
        return response;
      });
    };
    return function uninstall() {
      root.fetch = original;
    };
  }

  var api = { pfDecryptResponse: pfDecryptResponse, pfInstallFetch: pfInstallFetch };

  if (typeof module === "object" && module.exports) {
    module.exports = api;
  }
  if (root) {
    root.pfDecryptResponse = pfDecryptResponse;
    root.pfInstallFetch = pfInstallFetch;
  }
})(
  typeof window !== "undefined"
    ? window
    : typeof globalThis !== "undefined"
      ? globalThis
      : undefined
);
