document.addEventListener("DOMContentLoaded", () => {
  document.querySelectorAll(".rename-button").forEach(button => {
    button.addEventListener("click", () => {
      const fileID = button.dataset.fileId;
      const currentName = button.dataset.currentName;

      const newName = prompt("Enter new filename (include extension):", currentName);

      if (!newName) {
        return;
      }

      const form = document.getElementById(`renameForm-${fileID}`);
      form.querySelector('input[name="newfilename"]').value = newName;
      form.submit();
    });
  });
});
