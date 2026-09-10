import { describe, expect, it } from "vitest";

import type { StatusView } from "../../api/connection";
import type { ObjectRef } from "../../api/object";

import { showing } from "./regions";

function open(environment = ""): StatusView {
  return {
    id: "1",
    savedId: "saved-1",
    state: "connected",
    diagnosis: { failed: false, class: "", summary: "", cause: "", nextStep: "", detail: "" },
    name: "billing",
    environment,
    readOnly: false,
    host: "db.example.com",
    port: 5432,
    database: "app",
    user: "reporting",
  };
}

const object: ObjectRef = { database: "app", schema: "sales", name: "orders" };

describe("what the window shows", () => {
  it("shows nothing on the sides until a connection is open", () => {
    expect(showing(null, null)).toEqual({ objects: false, details: false, production: false });
  });

  // Everything on the sides belongs to a connection. A selection outliving the
  // connection it was made in is the state this rules out: the panel would ask
  // an identifier nobody has open for the properties of an object.
  it("shows no details for a selection with no connection behind it", () => {
    expect(showing(null, object).details).toBe(false);
  });

  it("shows the objects of an open connection", () => {
    expect(showing(open(), null)).toEqual({ objects: true, details: false, production: false });
  });

  it("shows the details of an object picked in an open connection", () => {
    expect(showing(open(), object).details).toBe(true);
  });

  it("marks a production connection", () => {
    expect(showing(open("prod"), null).production).toBe(true);
  });

  it("marks nothing else", () => {
    for (const environment of ["", "dev", "staging"]) {
      expect(showing(open(environment), null).production).toBe(false);
    }
  });

  // The mark is drawn on the window, so a mark with no connection behind it
  // would be a window claiming to be production with nothing open at all.
  it("marks nothing when there is no connection", () => {
    expect(showing(null, object).production).toBe(false);
  });
});
