document.addEventListener('DOMContentLoaded', function() {
    fetchQueues();

    document.getElementById('add-queue-form').addEventListener('submit', function(e) {
        e.preventDefault();
        const queueName = document.getElementById('queue-name').value;
        addQueue(queueName);
    });

    document.getElementById('get-message-form').addEventListener('submit', function(e) {
        e.preventDefault();
        const queue = document.getElementById('selected-queue').value;
        getMessage(queue);
    });
});

async function fetchQueues() {
    const response = await fetch('/api/queues');
    const data = await response.json();
    const queuesList = document.getElementById('queues-list');
    queuesList.innerHTML = '';
    for (const queue of data.queues) {     
        const row = document.createElement('tr');
        row.onclick = () => selectQueue(queue);
        row.innerHTML = `
            <td>${queue.vhost}</td>
            <td><b>${queue.name}</b></td>
            <td><span class="small-green-square"> </span> running</td>
            <td>${queue.messages}</td>
            <td>0</td>
            <td>0</td>
            <td>0</td>
            <td>
                <button class='delete-button' onclick="deleteQueue('${queue.name}')">Delete</button>
            </td>
        `;
        queuesList.appendChild(row);
    };
}

async function CountMessages(name) {
    const response = await fetch(`/api/queues/${name}/count`);
    const json = await response.json()
    return json.data.count
}

async function addQueue(name) {
    const queue = {
        queue_name: name
    }
    const response = await fetch('/api/queues', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify(queue)
    });
    if (response.ok) fetchQueues();
}

async function deleteQueue(name) {
    const response = await fetch(`/api/queues/${name}`, { method: 'DELETE' });
    if (response.ok) fetchQueues();
}

async function getMessage(queue) {
    const response = await fetch(`/api/queues/${queue}/consume`, {
        method: 'POST',
        headers: {'Content-Type': 'application/json'}
    });
    if (response.ok) {
        const json = await response.json();
        const message = json.data
        const messageContent = document.getElementById('message-content');
        messageContent.style.display = 'block';
        document.getElementById('message').textContent = message;
        fetchQueues();
    } else {
        console.error('Failed to get message');
    }
}

function selectQueue(queue) {
    document.getElementById('selected-queue').value = queue.name;
    document.getElementById('get-message').style.display = 'block';
}