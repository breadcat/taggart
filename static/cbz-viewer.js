// CBZ Keyboard Navigation

// Scroll to viewer on page load
window.addEventListener('DOMContentLoaded', function() {
	const viewer = document.querySelector('.cbz-viewer');
	if (viewer) {
		viewer.scrollIntoView({ behavior: 'instant', block: 'start' });
	}
});

document.addEventListener('keydown', function(e) {
	// Don't intercept keys if user is typing in an input/textarea
	const activeElement = document.activeElement;
	if (activeElement.tagName === 'INPUT' ||
	    activeElement.tagName === 'TEXTAREA' ||
	    activeElement.isContentEditable) {
		return;
	}

	// Get data from the .cbz-viewer element
	const viewer = document.querySelector('.cbz-viewer');
	if (!viewer) return;

	const currentIndex = parseInt(viewer.dataset.currentIndex);
	const totalImages = parseInt(viewer.dataset.totalImages);
	const fileID = viewer.dataset.fileId;

	switch(e.key) {
		case 'ArrowLeft':
		case 'a':
			if (currentIndex > 0) {
				window.location.href = `/cbz/${fileID}/${currentIndex - 1}`;
			}
			break;
		case 'ArrowRight':
		case 'd':
			if (currentIndex < totalImages - 1) {
				window.location.href = `/cbz/${fileID}/${currentIndex + 1}`;
			}
			break;
		case 'Home':
			window.location.href = `/cbz/${fileID}/0`;
			break;
		case 'End':
			window.location.href = `/cbz/${fileID}/${totalImages - 1}`;
			break;
	}
});