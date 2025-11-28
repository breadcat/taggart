// Initialize from the global variable passed from the template
let aliasGroups = window.initialAliasGroups || [];

function renderAliasGroups() {
    const container = document.getElementById('alias-groups');
    container.innerHTML = '';

    aliasGroups.forEach((group, groupIndex) => {
        const groupDiv = document.createElement('div');
        groupDiv.style.cssText = 'border: 1px solid #ddd; padding: 15px; margin-bottom: 15px; border-radius: 4px; background-color: #f8f9fa;';

        groupDiv.innerHTML = `
            <div style="margin-bottom: 10px;">
                <label style="display: block; font-weight: bold; margin-bottom: 5px;">Category:</label>
                <input type="text"
                       value="${escapeHtml(group.category)}"
                       onchange="updateCategory(${groupIndex}, this.value)"
                       style="width: 200px; padding: 6px; font-size: 14px;"
                       placeholder="e.g., colour">
            </div>

            <div style="margin-bottom: 10px;">
                <label style="display: block; font-weight: bold; margin-bottom: 5px;">Aliased Values:</label>
                <div id="aliases-${groupIndex}"></div>
                <button onclick="addAlias(${groupIndex})" type="button" class="text-button">+ Add Value</button>
            </div>

            <button onclick="removeAliasGroup(${groupIndex})" type="button" class="text-button">Remove Group</button>
        `;

        container.appendChild(groupDiv);
        renderAliases(groupIndex);
    });
}

function renderAliases(groupIndex) {
    const container = document.getElementById(`aliases-${groupIndex}`);
    container.innerHTML = '';

    const group = aliasGroups[groupIndex];
    if (!group.aliases) {
        group.aliases = [];
    }

    group.aliases.forEach((alias, aliasIndex) => {
        const aliasDiv = document.createElement('div');
        aliasDiv.style.cssText = 'display: flex; gap: 10px; margin-bottom: 5px; align-items: center;';

        aliasDiv.innerHTML = `
            <input type="text"
                   value="${escapeHtml(alias)}"
                   onchange="updateAlias(${groupIndex}, ${aliasIndex}, this.value)"
                   style="flex: 1; padding: 6px; font-size: 14px;"
                   placeholder="e.g., blue">
            <button onclick="removeAlias(${groupIndex}, ${aliasIndex})" type="button" class="text-button">Remove</button>
        `;

        container.appendChild(aliasDiv);
    });
}

function addAliasGroup() {
    aliasGroups.push({
        category: '',
        aliases: ['', '']
    });
    renderAliasGroups();
}

function removeAliasGroup(groupIndex) {
    if (confirm('Remove this alias group?')) {
        aliasGroups.splice(groupIndex, 1);
        renderAliasGroups();
    }
}

function updateCategory(groupIndex, value) {
    aliasGroups[groupIndex].category = value;
}

function addAlias(groupIndex) {
    aliasGroups[groupIndex].aliases.push('');
    renderAliases(groupIndex);
}

function removeAlias(groupIndex, aliasIndex) {
    aliasGroups[groupIndex].aliases.splice(aliasIndex, 1);
    renderAliases(groupIndex);
}

function updateAlias(groupIndex, aliasIndex, value) {
    aliasGroups[groupIndex].aliases[aliasIndex] = value;
}

function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}

document.getElementById('aliases-form').addEventListener('submit', function(e) {
    // Filter out empty groups and aliases
    const cleanedGroups = aliasGroups
        .filter(group => group.category && group.aliases && group.aliases.length > 0)
        .map(group => ({
            category: group.category.trim(),
            aliases: group.aliases.filter(a => a && a.trim()).map(a => a.trim())
        }))
        .filter(group => group.aliases.length >= 2); // Need at least 2 values to be an alias

    document.getElementById('aliases_json').value = JSON.stringify(cleanedGroups);
});

// Initial render
renderAliasGroups();
