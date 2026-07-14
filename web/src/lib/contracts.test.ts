import { describe, expect, it } from "vitest";
import type { ProjectCreateRequest, ProjectDTO, ProjectPatchRequest, Requirement } from "@/lib/contracts";

// Contract mirror: the web Requirement / ProjectDTO shapes must track Go's
// db.Requirement / project DTO (CLAUDE.md hand-mirror rule). These are
// compile-time-checked type assignments plus a runtime shape check; a drift in
// field names or optionality breaks `npm run typecheck` (and this test).
describe("Requirement contract mirror", () => {
  it("matches the Go db.Requirement json shape { id, text, check_cmd? }", () => {
    const full: Requirement = { id: "rls", text: "Always use RLS", check_cmd: "s.sh" };
    const minimal: Requirement = { id: "wcag", text: "WCAG 2.2 AA" }; // check_cmd optional
    expect(Object.keys(full).sort()).toEqual(["check_cmd", "id", "text"]);
    expect(minimal.check_cmd).toBeUndefined();
  });

  it("is carried (required) by the project DTO and (optional) by both request bodies", () => {
    const reqs: Requirement[] = [{ id: "pii", text: "No PII in logs" }];
    const dto: Pick<ProjectDTO, "requirements"> = { requirements: reqs };
    const create: ProjectCreateRequest = { name: "n", repo: "o/r", server_id: "h", workdir: "/w", requirements: reqs };
    const patch: ProjectPatchRequest = { requirements: reqs };
    expect(dto.requirements).toBe(reqs);
    expect(create.requirements).toBe(reqs);
    expect(patch.requirements).toBe(reqs);
  });
});
