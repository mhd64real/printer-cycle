// A printer-cycle connector, in one file with no dependencies.
//
// It connects, says what it is, declares one setting, and prints a document.
// That is the whole protocol: everything else a connector might do is more of
// the same calls.
//
// Nothing here imports anything from printer-cycle. It was written from
// PROTOCOL.md, which is the point: a connector is a separate program that talks
// to core over a socket, and it can be written in whatever language you like by
// somebody who has never read this repository.
//
//   node hello-printer.mjs --token PCE-XXXXX-XXXXX-XXXXX-XXXXX --print letter.pdf
//
// Node 22 or newer. WebSocket and Ed25519 are both built in.

import { createPrivateKey, createPublicKey, generateKeyPairSync, randomUUID, sign } from "node:crypto";
import { existsSync, readFileSync, statSync, writeFileSync, createReadStream } from "node:fs";

const arg = (name, fallback) => {
  const i = process.argv.indexOf(`--${name}`);
  return i === -1 ? fallback : process.argv[i + 1];
};

const CORE = arg("core", "ws://127.0.0.1:6310/v1/connector");
const KEY = arg("key", "hello-printer.key");
const ID = arg("id", "hello-printer");
const TOKEN = arg("token", null);
const DOCUMENT = arg("print", null);

// The keypair is this connector's identity. Core only ever sees the public
// half, so a copy of core's database is worth nothing to whoever took it.
if (!existsSync(KEY)) {
  const { privateKey } = generateKeyPairSync("ed25519");
  writeFileSync(KEY, privateKey.export({ type: "pkcs8", format: "pem" }), { mode: 0o600 });
}
const privateKey = createPrivateKey(readFileSync(KEY));
const publicKey = createPublicKey(privateKey).export({ type: "spki", format: "der" }).subarray(-32);

// Section 4: sign the fixed string, a zero byte, then the nonce. The prefix is
// what stops a hostile core collecting a signature that means something else.
const proofFor = (nonce) =>
  sign(null, Buffer.concat([
    Buffer.from("printer-cycle-connector-auth-v1", "ascii"),
    Buffer.from([0]),
    Buffer.from(nonce, "base64"),
  ]), privateKey).toString("base64");

const MANIFEST = {
  name: "Hello Printer",
  version: "1.0.0",
  description: "An example connector. Prints one document and stops.",
  identity: "none",
  settings: [
    { key: "greeting", type: "string", label: "Greeting", default: "hello" },
  ],
};

function connect(token) {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(CORE);
    const pending = new Map();

    // Section 3: JSON-RPC 2.0 in text frames, one message each.
    const call = (method, params = {}) =>
      new Promise((ok, no) => {
        const id = randomUUID();
        pending.set(id, { ok, no });
        ws.send(JSON.stringify({ jsonrpc: "2.0", id, method, params }));
      });

    ws.binaryType = "arraybuffer";

    ws.addEventListener("message", async ({ data }) => {
      if (typeof data !== "string") return; // core sends no binary frames

      const msg = JSON.parse(data);
      if (pending.has(msg.id)) {
        const { ok, no } = pending.get(msg.id);
        pending.delete(msg.id);
        msg.error ? no(new Error(`${msg.error.code} ${msg.error.message}`)) : ok(msg.result);
        return;
      }
      if (msg.method !== "hello") return;

      try {
        // Section 4: a new connector enrols its key, then reconnects, because
        // the challenge is spent by the attempt that carried the enrolment.
        if (token) {
          await call("enrol", { token, public_key: publicKey.toString("base64") });
          ws.close();
          return resolve("enrolled");
        }

        await call("authenticate", { connector_id: ID, proof: proofFor(msg.params.nonce) });
        const { settings } = await call("register", MANIFEST);
        console.log("connected. settings:", settings);

        if (DOCUMENT) await printDocument(call, ws);
        ws.close();
        resolve("done");
      } catch (err) {
        ws.close();
        reject(err);
      }
    });

    ws.addEventListener("error", (e) => reject(new Error(e.message ?? "socket error")));
  });
}

// Section 7: open a stream, send the bytes as binary frames, then commit.
//
// The document never sits in memory at either end. Each frame is the stream id
// as four big-endian bytes, then part of the file.
async function printDocument(call, ws) {
  const { printers } = await call("printers.list");
  if (!printers.length) throw new Error("no printers are set up yet");

  const { stream_id } = await call("jobs.submit", {
    printer_id: printers[0].id,
    document: { filename: DOCUMENT, mime: "application/pdf" },
  });

  const header = Buffer.alloc(4);
  header.writeUInt32BE(stream_id);

  for await (const chunk of createReadStream(DOCUMENT, { highWaterMark: 64 * 1024 })) {
    ws.send(Buffer.concat([header, chunk]));
  }

  // Core checks the length before anything reaches the printer, so a truncated
  // upload is refused rather than half printed.
  const job = await call("jobs.commit", {
    stream_id,
    bytes: statSync(DOCUMENT).size,
  });
  console.log("printed:", job);
}

if ((await connect(TOKEN)) === "enrolled") {
  console.log("enrolled. reconnecting to authenticate.");
  await connect(null);
}
