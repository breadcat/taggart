document.getElementById("copy-btn").addEventListener("click", function() {
    const text = document.getElementById("raw-url").textContent.trim();
    const status = document.getElementById("copy-status");

    // Fallback approach using a temporary textarea
    const textarea = document.createElement("textarea");
    textarea.value = text;
    textarea.style.position = "fixed"; // prevent scrolling
    textarea.style.left = "-9999px"; // off-screen
    document.body.appendChild(textarea);
    textarea.focus();
    textarea.select();

    let successful = false;
    try {
        successful = document.execCommand("copy");
    } catch (err) {
        successful = false;
    }

    document.body.removeChild(textarea);

    if (successful) {
        status.textContent = "✓ Copied!";
        status.style.color = "green";
    } else {
        status.textContent = "✗ Copy failed";
        status.style.color = "red";
    }
});