# Only names enter the matrix: Forgejo includes matrix values in status names.
if type != "array" or length == 0 then error("Missing CI results") else . end
| if all(.[];
    (.name | type == "string" and length > 0)
    and (.exitCode | type == "number")
    and (.status == "success" or .status == "error" or .status == "warning")
    and (.reason | type == "string")
  ) and ((map(.name) | unique | length) == length)
  then . else error("Invalid CI results") end
| "checks=\(map(.name) | tojson)",
  "results=\(map({key: .name, value: .}) | from_entries | tojson)"
