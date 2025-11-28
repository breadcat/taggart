document.addEventListener('DOMContentLoaded', function () {
  const fileRangeInput = document.getElementById('file_range');
  if (!fileRangeInput) return;

  const fileForm = fileRangeInput.closest('form');
  if (!fileForm) return;

  function updateValueField() {
    const checkedOp = fileForm.querySelector('input[name="operation"]:checked');
    const valueField = fileForm.querySelector('#value');
    const valueLabel = fileForm.querySelector('label[for="value"]');

    if (!checkedOp || !valueField || !valueLabel) return;

    if (checkedOp.value === 'add') {
        valueField.required = true;
        valueLabel.innerHTML = 'Value <span class="required">(required)</span>:';
    } else {
        valueField.required = false;
        valueLabel.innerHTML = 'Value:';
    }
}

// Set up event listeners for radio buttons inside this form
fileForm.querySelectorAll('input[name="operation"]').forEach(function (radio) {
    radio.addEventListener('change', updateValueField);
});

// Initialize on page load
updateValueField();

  // Add form validation ONLY to the fileForm (won't affect the search form)
  fileForm.addEventListener('submit', function (e) {
    const fileRange = (fileForm.querySelector('#file_range') || { value: '' }).value.trim();
    const category = (fileForm.querySelector('#category') || { value: '' }).value.trim();
    const value = (fileForm.querySelector('#value') || { value: '' }).value.trim();
    const checkedOp = fileForm.querySelector('input[name="operation"]:checked');
    const operation = checkedOp ? checkedOp.value : '';

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

    if (operation === 'add' && !value) {
        alert('Please enter a tag value when adding tags');
        e.preventDefault();
        return;
    }

    const rangePattern = /^[\d\s,-]+$/;
    if (!rangePattern.test(fileRange)) {
        alert('File range should only contain numbers, commas, dashes, and spaces');
        e.preventDefault();
        return;
    }
});
});