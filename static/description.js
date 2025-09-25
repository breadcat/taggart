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