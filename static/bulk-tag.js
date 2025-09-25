// Auto-focus the file range input
document.getElementById('file_range').focus();

// Update form behavior based on operation selection
function updateValueField() {
    const operation = document.querySelector('input[name="operation"]:checked').value;
    const valueField = document.getElementById('value');
    const valueLabel = document.querySelector('label[for="value"]');

    if (operation === 'add') {
        valueField.required = true;
        valueLabel.innerHTML = 'Value: <span style="color: red;">*</span>';
    } else {
        valueField.required = false;
        valueLabel.innerHTML = 'Value:';
    }
}

// Set up event listeners for radio buttons
document.querySelectorAll('input[name="operation"]').forEach(radio => {
    radio.addEventListener('change', updateValueField);
});

// Initialize on page load
updateValueField();

// Add form validation
document.querySelector('form').addEventListener('submit', function(e) {
    const fileRange = document.getElementById('file_range').value.trim();
    const category = document.getElementById('category').value.trim();
    const value = document.getElementById('value').value.trim();
    const operation = document.querySelector('input[name="operation"]:checked').value;

    if (!fileRange) {
        alert('Please enter a file ID range');
        e.preventDefault();
        return;
    }

    if (!category) {
        alert('Please enter a category');
        e.preventDefault();
        return;
    }

    // Only require value for add operations
    if (operation === 'add' && !value) {
        alert('Please enter a tag value when adding tags');
        e.preventDefault();
        return;
    }

    // Basic validation of range format
    const rangePattern = /^[\d\s,-]+$/;
    if (!rangePattern.test(fileRange)) {
        alert('File range should only contain numbers, commas, dashes, and spaces');
        e.preventDefault();
        return;
    }
});