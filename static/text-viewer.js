let originalText = '';

async function loadTextFile() {
  const viewer = document.getElementById("text-viewer");
  const filename = viewer.dataset.filename;
  const response = await fetch(`/uploads/${filename}`);
  const text = await response.text();
  originalText = text;
  viewer.textContent = text;
}

function toggleLineNumbers() {
  const viewer = document.getElementById("text-viewer");
  if (viewer.classList.contains("with-lines")) {
    viewer.classList.remove("with-lines");
    viewer.textContent = originalText; // Use stored original text
  } else {
    const lines = originalText.split("\n"); // Use stored original text
    viewer.innerHTML = lines.map((line, i) =>
      `<span style="display:block;"><span style="color:#888; user-select:none; width:3em; display:inline-block;">${i+1}</span> ${escapeHtml(line)}</span>`
    ).join("");
    viewer.classList.add("with-lines");
  }
}

function escapeHtml(text) {
  const div = document.createElement('div');
  div.textContent = text;
  return div.innerHTML;
}

function toggleFullscreen() {
  const container = document.getElementById("text-viewer-container");
  if (!document.fullscreenElement) {
    container.requestFullscreen();
  } else {
    document.exitFullscreen();
  }
}

function parseLineNumber(str) {
  // Match [l123] or [L123]
  const match = str.match(/^[lL](\d+)$/);
  return match ? Number(match[1]) : null;
}

function makeLineNumbersClickable(containerId, viewerId) {
  const container = document.getElementById(containerId);
  const viewer = document.getElementById(viewerId);

  // Regex: [l123] or [L123]
  const regex = /\[([lL]\d+)\]/g;

  container.innerHTML = container.innerHTML.replace(regex, (match, lineRef) => {
    const lineNum = parseLineNumber(lineRef);
    return `<a href="#" class="line-link" data-line="${lineNum}">${match}</a>`;
  });

  container.addEventListener("click", e => {
    if (e.target.classList.contains("line-link")) {
      e.preventDefault();
      const lineNum = Number(e.target.dataset.line);
      scrollToLine(lineNum);
    }
  });
}

function scrollToLine(lineNum) {
  const viewer = document.getElementById("text-viewer");

  // If line numbers are visible, find the specific line span
  if (viewer.classList.contains("with-lines")) {
    const lines = viewer.querySelectorAll("span[style*='display:block']");
    if (lines[lineNum - 1]) {
      lines[lineNum - 1].scrollIntoView({ behavior: "smooth", block: "center" });
      // Optional: highlight the line briefly
      lines[lineNum - 1].style.background = "#ff06";
      setTimeout(() => lines[lineNum - 1].style.background = "", 2000);
    }
  } else {
    // If no line numbers shown, calculate approximate position
    const lines = originalText.split("\n");
    const totalLines = lines.length;
    const percentage = (lineNum - 1) / totalLines;
    viewer.scrollTop = viewer.scrollHeight * percentage;
  }
}

// Run it after the page loads
document.addEventListener("DOMContentLoaded", () => {
  // Replace "description-container" with the ID of your description element
  makeLineNumbersClickable("current-description", "text-viewer");
});

    loadTextFile();
