function toggleDescriptionEdit() {
    const displayDiv = document.getElementById('description-display');
    const editDiv = document.getElementById('description-edit');

    displayDiv.style.display = 'none';
    editDiv.style.display = 'block';

    // Focus the textarea and update character count
    const textarea = document.getElementById('description-textarea');
    textarea.focus();

    // Move cursor to end of text if there's existing content
    if (textarea.value) {
        textarea.setSelectionRange(textarea.value.length, textarea.value.length);
    }
}

function cancelDescriptionEdit() {
    const displayDiv = document.getElementById('description-display');
    const editDiv = document.getElementById('description-edit');
    const textarea = document.getElementById('description-textarea');

    // Reset textarea to original value
    const original = displayDiv.dataset.originalDescription || '';
    textarea.value = original;

    displayDiv.style.display = 'block';
    editDiv.style.display = 'none';

    // Re-run conversion so any [file/123] becomes clickable again
    convertFileRefs();
}

// Auto-resize textarea as content changes
document.addEventListener('DOMContentLoaded', function() {
    const textarea = document.getElementById('description-textarea');
    if (textarea) {
        textarea.addEventListener('input', function() {
            // Reset height to auto to get the correct scrollHeight
            this.style.height = 'auto';
            // Set the height to match the content, with a minimum of 6 rows
            const minHeight = parseInt(getComputedStyle(this).lineHeight) * 6;
            this.style.height = Math.max(minHeight, this.scrollHeight) + 'px';
        });
    }
});

// Allow [file/123] and [/file/123] links to become clickable
function convertFileRefs() {
  const el = document.getElementById("current-description");
  if (!el) return;

  const pattern = /\[\/?file\/(\d+)\]/g;

  // Walk through text nodes only, preserving existing HTML elements
  function processTextNodes(node) {
    if (node.nodeType === Node.TEXT_NODE) {
      let text = node.textContent;

      // Check if this text node contains file references
      if (pattern.test(text)) {
        // Reset regex lastIndex
        pattern.lastIndex = 0;

        // Replace file references
        text = text.replace(pattern, (_, id) => {
          return `<a href="/file/${id}" class="file-link">file/${id}</a>`;
        });

        // Create a temporary container and replace the text node
        const temp = document.createElement('span');
        temp.innerHTML = text;
        const parent = node.parentNode;
        while (temp.firstChild) {
          parent.insertBefore(temp.firstChild, node);
        }
        parent.removeChild(node);
      }
    } else if (node.nodeType === Node.ELEMENT_NODE && node.tagName !== 'A') {
      // Don't process inside existing anchor tags
      Array.from(node.childNodes).forEach(processTextNodes);
    }
  }

  processTextNodes(el);
}

document.addEventListener("DOMContentLoaded", function() {
  convertFileRefs();
});