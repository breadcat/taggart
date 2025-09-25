const button = document.getElementById("searchToggle");
const div = document.getElementById("searchToggleContainer");
button.addEventListener("click", () => {
  div.style.display = (div.style.display === "none" || div.style.display === "")
    ? "block"
    : "none";
});