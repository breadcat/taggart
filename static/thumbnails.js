function showTab(tabName) {
    // Hide all content
    document.getElementById('content-missing').style.display = 'none';
    document.getElementById('content-all').style.display = 'none';

    // Remove active styling from all tabs
    document.getElementById('tab-missing').style.borderBottomColor = 'transparent';
    document.getElementById('tab-missing').style.fontWeight = 'normal';
    document.getElementById('tab-all').style.borderBottomColor = 'transparent';
    document.getElementById('tab-all').style.fontWeight = 'normal';

    // Show selected content and style tab
    document.getElementById('content-' + tabName).style.display = 'block';
    document.getElementById('tab-' + tabName).style.borderBottomColor = '#007bff';
    document.getElementById('tab-' + tabName).style.fontWeight = 'bold';
}

// Auto-hide success messages
function autoHideSuccess() {
    const successDivs = document.querySelectorAll('.auto-hide-success');
    successDivs.forEach(div => {
        setTimeout(() => {
            div.style.transition = 'opacity 0.5s';
            div.style.opacity = '0';
            setTimeout(() => div.remove(), 500);
        }, 5000);
    });
}

// Call auto-hide
autoHideSuccess();

// Add video preview on hover
document.querySelectorAll('video').forEach(video => {
    video.addEventListener('mouseenter', function() {
        this.play();
    });
    video.addEventListener('mouseleave', function() {
        this.pause();
        this.currentTime = 0;
    });
});

// Add timestamp helper
document.querySelectorAll('input[name="timestamp"]').forEach(input => {
    const video = input.closest('div[style*="border"]').querySelector('video');
    if (video) {
        input.addEventListener('focus', function() {
            video.play();
        });

        // Click on video to set current time as timestamp
        video.addEventListener('click', function(e) {
            e.preventDefault();
            const currentTime = this.currentTime;
            const hours = Math.floor(currentTime / 3600);
            const minutes = Math.floor((currentTime % 3600) / 60);
            const seconds = Math.floor(currentTime % 60);
            const formatted = `${String(hours).padStart(2, '0')}:${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}`;
            const timestampInput = this.closest('div[style*="border"]').querySelector('input[name="timestamp"]');
            if (timestampInput) {
                timestampInput.value = formatted;
                // Flash the input to show it updated
                timestampInput.style.backgroundColor = '#ffffcc';
                setTimeout(() => {
                    timestampInput.style.backgroundColor = '';
                }, 500);
            }
            this.pause();
        });
    }
});