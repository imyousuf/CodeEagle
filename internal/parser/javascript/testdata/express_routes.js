const express = require('express');
const { getUsers, createUser, updateUser, deleteUser } = require('./handlers');

const app = express();
const router = express.Router();

router.get('/users', getUsers);
router.post('/users', createUser);
router.put('/users/:id', updateUser);
router.delete('/users/:id', deleteUser);
router.patch('/users/:id', function(req, res) {
  res.json({ patched: true });
});

app.use('/api/v1', router);

app.get('/health', function(req, res) {
  res.json({ status: 'ok' });
});

module.exports = app;
