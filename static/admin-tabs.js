// Admin tab management
function showAdminTab(tabName) {
    // Hide all content sections
    const contents = ['settings', 'database', 'aliases', 'orphans', 'thumbnails'];
    contents.forEach(name => {
        const content = document.getElementById(`admin-content-${name}`);
        if (content) {
            content.style.display = 'none';
        }
    });

    // Remove active styling from all tabs
    document.querySelectorAll('.admin-tab-btn').forEach(btn => {
        btn.style.borderBottomColor = 'transparent';
        btn.style.fontWeight = 'normal';
    });

    // Show selected content
    const selectedContent = document.getElementById(`admin-content-${tabName}`);
    if (selectedContent) {
        selectedContent.style.display = 'block';
    }

    // Activate selected tab
    const selectedTab = document.getElementById(`admin-tab-${tabName}`);
    if (selectedTab) {
        selectedTab.style.borderBottomColor = '#007bff';
        selectedTab.style.fontWeight = 'bold';
    }

    // Store active tab in session storage
    sessionStorage.setItem('activeAdminTab', tabName);
}

// Thumbnail sub-tab management
function showThumbnailSubTab(subTabName) {
    // Hide all sub-tab contents
    const subContents = ['missing', 'regenerate'];
    subContents.forEach(name => {
        const content = document.getElementById(`thumb-content-${name}`);
        if (content) {
            content.style.display = 'none';
        }
    });

    // Remove active styling from all sub-tabs
    document.querySelectorAll('.thumb-subtab-btn').forEach(btn => {
        btn.style.borderBottomColor = 'transparent';
        btn.style.fontWeight = 'normal';
    });

    // Show selected sub-tab content
    const selectedContent = document.getElementById(`thumb-content-${subTabName}`);
    if (selectedContent) {
        selectedContent.style.display = 'block';
    }

    // Activate selected sub-tab
    const selectedTab = document.getElementById(`thumb-subtab-${subTabName}`);
    if (selectedTab) {
        selectedTab.style.borderBottomColor = '#007bff';
        selectedTab.style.fontWeight = 'bold';
    }

    // Store active sub-tab in session storage
    sessionStorage.setItem('activeThumbnailSubTab', subTabName);
}

// Initialize on page load
document.addEventListener('DOMContentLoaded', function() {
    // Restore previous tab selection or default to settings
    const savedTab = sessionStorage.getItem('activeAdminTab') || 'settings';
    showAdminTab(savedTab);

    // Restore previous thumbnail sub-tab or default to missing
    const savedSubTab = sessionStorage.getItem('activeThumbnailSubTab') || 'missing';
    showThumbnailSubTab(savedSubTab);

    // Auto-hide success messages after 5 seconds
    const successDivs = document.querySelectorAll('.auto-hide-success');
    successDivs.forEach(div => {
        setTimeout(() => {
            div.style.transition = 'opacity 0.5s';
            div.style.opacity = '0';
            setTimeout(() => div.remove(), 500);
        }, 5000);
    });
});