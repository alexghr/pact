const form = document.querySelector("#repository-form");

if (form) {
  const repositoryURL = form.elements.url;
  const cloneURL = form.elements.clone_url;
  const pushURL = form.elements.push_url;
  const name = form.elements.name;

  let cloneFollowsRepository = cloneURL.value === "";
  let pushFollowsClone = pushURL.value === "";
  let nameFollowsClone = name.value === "";

  const nameFromCloneURL = (value) => {
    const trimmed = value.trim().replace(/\/+$/, "");
    const separator = Math.max(trimmed.lastIndexOf("/"), trimmed.lastIndexOf(":"));
    const segment = trimmed.slice(separator + 1);
    return segment.endsWith(".git") ? segment.slice(0, -4) : segment;
  };

  const updateCloneDependants = () => {
    if (pushFollowsClone) {
      pushURL.value = cloneURL.value;
    }
    if (nameFollowsClone) {
      name.value = nameFromCloneURL(cloneURL.value);
    }
  };

  repositoryURL.addEventListener("input", () => {
    if (cloneFollowsRepository) {
      cloneURL.value = repositoryURL.value;
      updateCloneDependants();
    }
  });
  cloneURL.addEventListener("input", () => {
    cloneFollowsRepository = false;
    updateCloneDependants();
  });
  pushURL.addEventListener("input", () => {
    pushFollowsClone = false;
  });
  name.addEventListener("input", () => {
    nameFollowsClone = false;
  });
}
